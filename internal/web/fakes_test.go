package web

import (
	"context"
	"sort"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/ragabast/internal/vector"
)

// fakeHumaService is a richer fake used by the Huma API tests.
// In addition to the per-method response overrides fakeService
// supports, it records what was called with what (last
// ingest content, last query options, deleted IDs, last
// frontmatter response) so tests can assert on the service's
// inputs without intercepting them.
//
// Lives next to fakeService so all web fakes are in one file;
// both are package-private because nothing outside the test
// suite should construct them.
type fakeHumaService struct {
	healthErr         error
	ingested          *models.Document
	ingestErr         error
	lastIngestContent string
	ingestCalls       int
	searchResults     []models.SearchResult
	searchErr         error
	documents         []models.DocumentInfo
	deleteErr         error
	deletedIDs        []string
	listCalls         int
	answer            string
	debug             *service.QueryDebugInfo
	queryErr          error
	lastQueryOpts     service.LLMOptions
	history           []service.ChatMessage // issue #22: canned prior history returned to the chat handler
	transcriptByID    map[string]string     // issue #39: canned markdown transcript keyed by session_id
	appendedTurns     []appendedTurn        // issue #22: recorded exchanges the chat handler asked us to remember
	frontmatterSug    service.FrontmatterSuggestion
	frontmatterErr    error
}

func (f *fakeHumaService) CheckHealth(_ context.Context) (bool, error) {
	return f.healthErr == nil, f.healthErr
}

func (f *fakeHumaService) IngestDocument(_ context.Context, content string) (*models.Document, error) {
	f.ingestCalls++
	if f.ingestErr != nil {
		return nil, f.ingestErr
	}
	f.lastIngestContent = content
	if f.ingested != nil {
		return f.ingested, nil
	}
	return &models.Document{ID: "doc-1", Chunks: []models.Chunk{{}, {}}}, nil
}

// callCount reports how many times IngestDocument has been
// invoked on this fake. The async-ingest tests use it to
// pin that the worker pool called the service exactly once
// per submitted job.
func (f *fakeHumaService) callCount() int {
	return f.ingestCalls
}

func (f *fakeHumaService) Search(_ context.Context, _ string, _ int, _ service.SearchFilters) ([]models.SearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

// HybridSearch mirrors Search for the v0.4.0 hybrid-routing
// path. Both fakes share the same canned results so existing
// search tests that don't care about the mode still work;
// tests that need to assert the mode is plumbed through can
// capture f.lastSearchMode via a small wrapper if needed.
func (f *fakeHumaService) HybridSearch(_ context.Context, _ string, _ int, _ service.SearchFilters, _ service.SearchMode) ([]models.SearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeHumaService) ListDocuments(_ context.Context) ([]models.DocumentInfo, error) {
	f.listCalls++
	return f.documents, nil
}

// ListDocumentsPaged returns a slice of f.documents (sorted
// by ID for deterministic ordering across requests) along
// with the full corpus size. limit <= 0 means "no limit";
// offset is clamped to [0, total] and negative offsets
// are treated as 0 — same rules the production handler
// applies. Sorts a copy so subsequent calls return a
// fresh slice and the test fixture doesn't accumulate
// cross-test state.
func (f *fakeHumaService) ListDocumentsPaged(_ context.Context, limit, offset int) ([]models.DocumentInfo, int, error) {
	f.listCalls++
	// Make a sorted copy of f.documents so we don't mutate
	// the test fixture's input across calls.
	sorted := make([]models.DocumentInfo, len(f.documents))
	copy(sorted, f.documents)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	total := len(sorted)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := total
	if limit > 0 {
		end = min(offset+limit, total)
	}
	return sorted[offset:end], total, nil
}

func (f *fakeHumaService) DeleteDocument(_ context.Context, documentID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	// Mirror the real vector behavior: when the document
	// isn't in the list, return vector.ErrNotFound rather
	// than silently succeeding. Lets the delete-path test
	// exercise both 200 (known ID) and 404 (unknown ID)
	// without standing up a real chromem-go collection.
	for _, d := range f.documents {
		if d.ID == documentID {
			f.deletedIDs = append(f.deletedIDs, documentID)
			return nil
		}
	}
	return vector.ErrNotFound
}

// BulkUpdateDocuments applies each patch to f.documents and
// returns a per-item result. Missing documents produce a
// per-item error rather than aborting the batch — mirrors
// the real service.BulkUpdateDocuments behavior. Empty
// document_ids surface as per-item errors (the real service
// rejects them upfront with ErrInvalidInput; the fake's
// per-item path is the most faithful test).
func (f *fakeHumaService) BulkUpdateDocuments(_ context.Context, patches []vector.DocumentMetadataPatch, mode string) ([]service.BulkUpdateDocumentsResult, error) {
	if len(patches) == 0 {
		return nil, models.ErrInvalidInput
	}
	out := make([]service.BulkUpdateDocumentsResult, len(patches))
	for i, patch := range patches {
		out[i].DocumentID = patch.DocumentID
		if patch.DocumentID == "" {
			out[i].Status = "error"
			out[i].Error = "empty document_id"
			continue
		}
		found := false
		applyPatch := func(j int, d models.DocumentInfo) bool {
			found = true
			if patch.Tags != nil {
				if mode == "replace" {
					f.documents[j].Tags = append([]string{}, patch.Tags...)
				} else {
					f.documents[j].Tags = mergeStrings(d.Tags, patch.Tags)
				}
			}
			if patch.Categories != nil {
				f.documents[j].Categories = append([]string{}, patch.Categories...)
			}
			if patch.URLs != nil {
				f.documents[j].URLs = append([]string{}, patch.URLs...)
			}
			out[i].Status = "ok"
			return true
		}
		for j, d := range f.documents {
			if d.ID == patch.DocumentID {
				if applyPatch(j, d) {
					break
				}
			}
		}
		if !found {
			out[i].Status = "error"
			out[i].Error = "document not found"
		}
	}
	return out, nil
}

// mergeStrings dedupes a union of two string slices.
// Local to the fake so the production code path stays
// free of test-only helpers.
func mergeStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func (f *fakeHumaService) QueryDebugWithOptions(_ context.Context, _ string, _ int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error) {
	f.lastQueryOpts = opts
	if f.queryErr != nil {
		return "", nil, f.queryErr
	}
	if f.debug == nil {
		f.debug = &service.QueryDebugInfo{Results: []models.SearchResult{}}
	}
	if f.answer == "" {
		f.answer = "Answer.\n\nLinks:\n- https://example.com/a"
	}
	return f.answer, f.debug, nil
}

func (f *fakeHumaService) ChatSessionHistory(string) []service.ChatMessage {
	if f.history != nil {
		return f.history
	}
	return []service.ChatMessage{}
}

func (f *fakeHumaService) ExportChatTranscript(sessionID string) string {
	if f.transcriptByID != nil {
		return f.transcriptByID[sessionID]
	}
	return ""
}

func (f *fakeHumaService) AppendChatTurn(_ context.Context, sessionID string, exchange ...service.ChatMessage) error {
	f.appendedTurns = append(f.appendedTurns, appendedTurn{sessionID: sessionID, messages: exchange})
	return nil
}

func (f *fakeHumaService) ClearChatSession(string) {}

func (f *fakeHumaService) SuggestFrontmatter(_ context.Context, _ string, _ map[string]any, _ []string, _ []string) (service.FrontmatterSuggestion, error) {
	if f.frontmatterErr != nil {
		return service.FrontmatterSuggestion{}, f.frontmatterErr
	}
	return f.frontmatterSug, nil
}

func (f *fakeHumaService) GetNormalizedTags(_ context.Context) ([]string, error) {
	return []string{"go", "rag", "api"}, nil
}

func (f *fakeHumaService) GetNormalizedCategories(_ context.Context) ([]string, error) {
	return []string{"Guides", "Reference", "Tutorials"}, nil
}

func (f *fakeHumaService) GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error) {
	tags, err = f.GetNormalizedTags(ctx)
	if err != nil {
		return nil, nil, err
	}
	categories, err = f.GetNormalizedCategories(ctx)
	if err != nil {
		return nil, nil, err
	}
	return tags, categories, nil
}

// QueryCacheStats satisfies serviceAPI for /api/health/full
// tests. Reports the cache disabled (capacity 0) so existing
// tests don't have to wire cache plumbing; new tests
// (TestHealthFull_ReportsCacheStats) configure a real
// service with a real cache.
func (f *fakeHumaService) QueryCacheStats() service.QueryCacheStats {
	return service.QueryCacheStats{}
}

// fakeService is a shared test double that implements the
// serviceAPI interface (defined in server.go). It serves every
// web test: per-test fields are set on the struct and the
// corresponding methods return them. Methods that a test does
// not care about return a harmless zero value.
//
// Fields:
//   - searchResults:    returned by Search
//   - queryAnswer:      returned as the answer in QueryDebugWithOptions
//   - queryDebug:       returned as the debug info; lazy-initialized to
//     an empty QueryDebugInfo if nil
//   - queryErr:         returned as the error from QueryDebugWithOptions,
//     taking precedence over queryAnswer/queryDebug
//   - checkHealthOK:    when false, CheckHealth returns an error
//   - ingestDocument:   returned by IngestDocument (defaults to a
//     stub doc with ID "doc-1" when nil)
//   - ingestErr:        returned by IngestDocument when non-nil
//   - tags/categories:  returned by the catalog lookups; defaults
//     match the legacy fakes so existing tests pass
type fakeService struct {
	searchResults     []models.SearchResult
	searchErr         error
	lastSearchQuery   string
	lastSearchFilters service.SearchFilters
	lastSearchLimit   int
	lastSearchMode    service.SearchMode
	queryAnswer       string
	queryDebug        *service.QueryDebugInfo
	history           []service.ChatMessage
	transcriptByID    map[string]string // issue #39: per-session canned export
	appendedTurns     []appendedTurn
	clearedSessions   []string
	queryErr          error
	checkHealthOK     bool
	ingestDocument    *models.Document
	ingestErr         error
	tags              []string
	categories        []string
	documents         []models.DocumentInfo
	listErr           error
}

// appendedTurn is the record the chat handler asks the
// service to remember for the next request. Tests use the
// slice to assert the right messages were threaded in
// (and to verify the assistant's reply is included, not
// just the user's question).
type appendedTurn struct {
	sessionID string
	messages  []service.ChatMessage
}

func (f *fakeService) CheckHealth(context.Context) (bool, error) {
	if !f.checkHealthOK {
		return false, errFakeUnhealthy
	}
	return true, nil
}

func (f *fakeService) IngestDocument(_ context.Context, _ string) (*models.Document, error) {
	if f.ingestErr != nil {
		return nil, f.ingestErr
	}
	if f.ingestDocument != nil {
		return f.ingestDocument, nil
	}
	return &models.Document{ID: "doc-1"}, nil
}

// Search records the most-recent (query, limit, filters) call so the
// web form-submit tests can assert that filter form fields actually
// reach the service. Returns searchResults unless searchErr is set.
func (f *fakeService) Search(_ context.Context, query string, limit int, filters service.SearchFilters) ([]models.SearchResult, error) {
	f.lastSearchQuery = query
	f.lastSearchFilters = filters
	f.lastSearchLimit = limit
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

// HybridSearch is the v0.4.0 entry point; records the mode
// alongside query/filters/limit so the v0.4.0 tests can
// assert that mode is plumbed through.
func (f *fakeService) HybridSearch(_ context.Context, query string, limit int, filters service.SearchFilters, mode service.SearchMode) ([]models.SearchResult, error) {
	f.lastSearchQuery = query
	f.lastSearchFilters = filters
	f.lastSearchLimit = limit
	f.lastSearchMode = mode
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeService) ListDocuments(context.Context) ([]models.DocumentInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.documents != nil {
		return f.documents, nil
	}
	return []models.DocumentInfo{}, nil
}

// ListDocumentsPaged is the pagination-aware companion to
// ListDocuments on fakeService. Most legacy tests use
// fakeService — the new huma tests use fakeHumaService —
// but both must satisfy the serviceAPI interface so the
// compile-time assertion in api.go holds.
func (f *fakeService) ListDocumentsPaged(context.Context, int, int) ([]models.DocumentInfo, int, error) {
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	if f.documents != nil {
		return f.documents, len(f.documents), nil
	}
	return []models.DocumentInfo{}, 0, nil
}

func (f *fakeService) DeleteDocument(context.Context, string) error {
	return nil
}

// BulkUpdateDocuments satisfies serviceAPI for the
// legacy fake (used by older tests). No-op: tests that
// exercise the bulk-update path use fakeHumaService.
func (f *fakeService) BulkUpdateDocuments(context.Context, []vector.DocumentMetadataPatch, string) ([]service.BulkUpdateDocumentsResult, error) {
	return nil, nil
}

func (f *fakeService) SuggestFrontmatter(context.Context, string, map[string]any, []string, []string) (service.FrontmatterSuggestion, error) {
	return service.FrontmatterSuggestion{}, nil
}

func (f *fakeService) QueryDebugWithOptions(_ context.Context, _ string, _ int, _ service.LLMOptions) (string, *service.QueryDebugInfo, error) {
	if f.queryErr != nil {
		return "", nil, f.queryErr
	}
	if f.queryDebug == nil {
		f.queryDebug = &service.QueryDebugInfo{Results: []models.SearchResult{}}
	}
	return f.queryAnswer, f.queryDebug, nil
}

func (f *fakeService) ChatSessionHistory(string) []service.ChatMessage {
	if f.history != nil {
		return f.history
	}
	return []service.ChatMessage{}
}

func (f *fakeService) ExportChatTranscript(sessionID string) string {
	if f.transcriptByID != nil {
		return f.transcriptByID[sessionID]
	}
	return ""
}

func (f *fakeService) AppendChatTurn(_ context.Context, sessionID string, exchange ...service.ChatMessage) error {
	f.appendedTurns = append(f.appendedTurns, appendedTurn{sessionID: sessionID, messages: exchange})
	return nil
}

func (f *fakeService) ClearChatSession(sessionID string) {
	f.clearedSessions = append(f.clearedSessions, sessionID)
}

func (f *fakeService) GetNormalizedTags(context.Context) ([]string, error) {
	if f.tags != nil {
		return f.tags, nil
	}
	return []string{"go", "rag", "api"}, nil
}

func (f *fakeService) GetNormalizedCategories(context.Context) ([]string, error) {
	if f.categories != nil {
		return f.categories, nil
	}
	return []string{"Guides", "Reference", "Tutorials"}, nil
}

func (f *fakeService) GetTagsAndCategories(ctx context.Context) ([]string, []string, error) {
	tags, err := f.GetNormalizedTags(ctx)
	if err != nil {
		return nil, nil, err
	}
	categories, err := f.GetNormalizedCategories(ctx)
	if err != nil {
		return nil, nil, err
	}
	return tags, categories, nil
}

// QueryCacheStats satisfies serviceAPI; reports zero values
// (cache disabled) so existing tests don't have to wire
// cache plumbing.
func (f *fakeService) QueryCacheStats() service.QueryCacheStats {
	return service.QueryCacheStats{}
}

// errFakeUnhealthy is the sentinel returned by CheckHealth when
// the test wants the unhealthy path. Kept private — tests set
// checkHealthOK=false to trigger it.
var errFakeUnhealthy = fakeError("service unhealthy")

type fakeError string

func (e fakeError) Error() string { return string(e) }
