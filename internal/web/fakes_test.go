package web

import (
	"context"
	"errors"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// vectorErrNotFound mirrors vector.ErrNotFound for the fake
// service. The web package doesn't currently import vector
// (the production code only talks to it through the
// serviceAPI interface), so importing it just for tests
// would create a coupling that doesn't exist in the
// production code path. Keeping a local copy of the
// sentinel keeps the test self-contained; the production
// handler will use errors.Is(err, vector.ErrNotFound) to
// detect the real one once it's wired up.
var vectorErrNotFound = errors.New("document not found")

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

func (f *fakeHumaService) DeleteDocument(_ context.Context, documentID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	// Mirror the real vector behavior: when the document
	// isn't in the list, return ErrNotFound rather than
	// silently succeeding. Lets the delete-path test
	// exercise both 200 (known ID) and 404 (unknown ID)
	// without standing up a real chromem-go collection.
	for _, d := range f.documents {
		if d.ID == documentID {
			f.deletedIDs = append(f.deletedIDs, documentID)
			return nil
		}
	}
	return vectorErrNotFound
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
	queryErr          error
	checkHealthOK     bool
	ingestDocument    *models.Document
	ingestErr         error
	tags              []string
	categories        []string
	documents         []models.DocumentInfo
	listErr           error
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

func (f *fakeService) DeleteDocument(context.Context, string) error {
	return nil
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

// errFakeUnhealthy is the sentinel returned by CheckHealth when
// the test wants the unhealthy path. Kept private — tests set
// checkHealthOK=false to trigger it.
var errFakeUnhealthy = fakeError("service unhealthy")

type fakeError string

func (e fakeError) Error() string { return string(e) }
