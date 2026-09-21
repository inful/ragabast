package service

import (
	"context"
	"fmt"
	"log"
	"maps"
	"strings"
	"sync"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service/querycache"
	"github.com/ragabast/internal/vector"
)

// SearchFilters narrows a Search by document-level attributes.
// Re-exported from the vector package so callers do not have to
// import vector directly. The full docstring lives on
// vector.SearchFilters — keep changes there.
type SearchFilters = vector.SearchFilters

// QueryDebugInfo describes what was retrieved and sent to the LLM.
type QueryDebugInfo struct {
	Model   string
	Results []models.SearchResult
	Context string
	System  string
	Prompt  string
}

// LLMOptions controls generation parameters.
type LLMOptions struct {
	Temperature *float64
	History     []ChatMessage
}

// Search performs a semantic search, optionally narrowed by
// document-level filters.
//
// Deprecated for hybrid ranking: use Service.HybridSearch with
// mode=hybrid (the v0.4.0 default) when callers want exact-term
// recall to complement embedding similarity. Kept for callers
// that need pure semantic behavior and for the search-tests
// that pin v0.3.0 semantics.
func (s *Service) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]models.SearchResult, error) {
	key := querycache.KeyOf(querycache.Key{
		Query:    query,
		DocID:    filters.DocumentID,
		Tag:      filters.Tag,
		Category: filters.Category,
		Limit:    limit,
		Mode:     "semantic", // Search is the v0.3.0 semantic-only path
		Model:    s.embedModel,
	})
	results, _, err := s.cache.Get(ctx, key, func(ctx context.Context) ([]models.SearchResult, error) {
		return s.vectorOps.Search(ctx, query, limit, filters.ToWhere())
	})
	if err != nil {
		return nil, wrapCorruptionError(err)
	}
	// Post-filter by date range (issue #38). chromem-go's
	// Where filter is equality-only, so range comparisons
	// happen here against the document-level dates that
	// VectorDB.Search populates from chunk metadata.
	results = applyDateFilters(results, filters)
	s.enrichWithDocbuilderURLs(results)
	return results, nil
}

// HybridSearch runs the configured search mode — keyword,
// semantic, or hybrid (the v0.4.0 default). The HTTP API and
// the `ragabast search` CLI both default to ModeHybrid; the
// in-process Service.Search retains pure-semantic semantics
// for callers that depend on them.
//
// Filters are applied to BOTH rankings (keyword side: term/match
// query against the document_id / tags / categories fields;
// semantic side: chromem-go Where filter on the corresponding
// metadata keys).
func (s *Service) HybridSearch(
	ctx context.Context,
	query string,
	limit int,
	filters SearchFilters,
	mode SearchMode,
) ([]models.SearchResult, error) {
	key := querycache.KeyOf(querycache.Key{
		Query:    query,
		DocID:    filters.DocumentID,
		Tag:      filters.Tag,
		Category: filters.Category,
		Limit:    limit,
		Mode:     mode.String(),
		Model:    s.embedModel,
	})
	results, _, err := s.cache.Get(ctx, key, func(ctx context.Context) ([]models.SearchResult, error) {
		return s.vectorOps.SearchHybrid(ctx, query, limit, filters, mode)
	})
	if err != nil {
		return nil, wrapCorruptionError(err)
	}
	// Post-filter by date range (issue #38). Same code
	// path as Search; the filter applies regardless of
	// mode (keyword, semantic, hybrid) so operators don't
	// have to think about which endpoint honors it.
	results = applyDateFilters(results, filters)
	s.enrichWithDocbuilderURLs(results)
	return results, nil
}

// enrichWithDocbuilderURLs populates the DocbuilderURL field on
// every result that has a non-empty UID. Results without a UID
// (or with an empty configured base URL) are left untouched.
// Safe to call multiple times — idempotent.
func (s *Service) enrichWithDocbuilderURLs(results []models.SearchResult) {
	if s.config.Ragabast.DocbuilderBaseURL == "" {
		return
	}
	for i := range results {
		results[i].DocbuilderURL = s.buildDocbuilderURL(results[i].UID)
	}
}

// SearchByDocument searches within a specific document.
func (s *Service) SearchByDocument(ctx context.Context, query string, documentID string, limit int) ([]models.SearchResult, error) {
	return s.Search(ctx, query, limit, SearchFilters{DocumentID: documentID})
}

// SearchMode is the public re-export of vector.SearchMode so
// callers (HTTP handlers, CLI) can refer to mode constants
// without importing the lower-level vector package directly.
type SearchMode = vector.SearchMode

// ModeHybrid, ModeSemantic, ModeKeyword are exposed at the
// service package level so HTTP and CLI callers can refer to
// them as service.ModeHybrid rather than reaching into the
// vector package.
const (
	ModeHybrid   = vector.ModeHybrid
	ModeSemantic = vector.ModeSemantic
	ModeKeyword  = vector.ModeKeyword
)

// deprecatedChatOnce guards the warning so it fires at most
// once per method name across the whole process. Without this
// the log would spam on every request. Adding a new chat-mode
// method? Just call warnDeprecated with the method name.
var deprecatedChatOnce sync.Map // map[string]*sync.Once

// resetDeprecatedForTest clears the per-method deprecation
// log-once state. Test-only: lets the deprecation test run
// repeatedly within the same process (e.g. `go test -count=N`)
// without the second-and-later runs silently passing because
// the warning already fired.
func resetDeprecatedForTest() {
	deprecatedChatOnce.Range(func(k, _ any) bool {
		deprecatedChatOnce.Delete(k)
		return true
	})
}

// warnDeprecated logs a one-shot deprecation notice pointing at
// the find-docs replacement. It does not change behavior; the
// deprecated method still runs. The notice is a soft nudge.
func warnDeprecated(method string) {
	v, _ := deprecatedChatOnce.LoadOrStore(method, &sync.Once{})
	once := v.(*sync.Once)
	once.Do(func() {
		log.Printf("DEPRECATED: Service.%s synthesizes an LLM answer; ragabast's primary mode is now find-docs, prefer Service.Search with filters and Service.FindDocuments. This method will be removed in a future release.", method)
	})
}

// Query performs a search and generates a natural language response using LLM.
//
// Deprecated: ragabast's primary mode is "point users to the right
// documentation page". Prefer Service.Search (with filters) or
// Service.FindDocuments for ranked document hits, and ask the
// LLM to synthesize only when you really need prose. The chat-mode
// surface remains available but will be removed in a future
// release; a one-shot runtime warning fires on first use.
func (s *Service) Query(ctx context.Context, query string, limit int) (string, error) {
	warnDeprecated("Query")
	response, _, err := s.QueryDebugWithOptions(ctx, query, limit, LLMOptions{})
	return response, err
}

// QueryDebugWithOptions performs a search and generates a response, returning
// debug information about the retrieved chunks and constructed prompt.
//
// Deprecated: see Service.Query.
func (s *Service) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts LLMOptions) (string, *QueryDebugInfo, error) {
	warnDeprecated("QueryDebugWithOptions")
	results, err := s.vectorOps.Search(ctx, query, limit, nil)
	if err != nil {
		return "", nil, fmt.Errorf("search failed: %w", err)
	}

	// Populate DocbuilderURL on every result so the chat handler's
	// InlineSourceLinks post-processor can convert the LLM's
	// [src:N] markers into clickable links. Without this call,
	// the post-processor falls into the "no URL → bare title text"
	// branch and the user sees source titles concatenated with no
	// links — the bug reported on 2026-09-20. Mirrors what
	// Service.Search does at line 74; QueryDebugWithOptions uses
	// vectorOps.Search directly so it has to do the enrichment
	// itself.
	s.enrichWithDocbuilderURLs(results)

	if len(results) == 0 {
		return "No relevant information found.", nil, nil
	}

	model := s.config.Ollama.ChatModel

	llmOptions := map[string]any{}
	maps.Copy(llmOptions, s.config.Ollama.Options)
	if opts.Temperature != nil {
		llmOptions["temperature"] = *opts.Temperature
	} else if s.config.Ollama.Temperature != nil {
		llmOptions["temperature"] = *s.config.Ollama.Temperature
	}

	contextItems := buildQueryContextItems(ctx, results, vectorChunkFetcher{s: s})
	messages, err := buildQueryMessages(query, contextItems, opts.History)
	if err != nil {
		return "", nil, fmt.Errorf("prompt build failed: %w", err)
	}

	response, err := s.llmClient.Chat(ctx, messages, llmOptions)
	if err != nil {
		return "", nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	response = appendLinksSection(response, extractURLs(results))

	return response, &QueryDebugInfo{
		Model:   model,
		Results: results,
		Context: strings.Join(contextItems, "\n\n"),
		System:  messages[0].Content,
		Prompt:  messages[1].Content,
	}, nil
}
