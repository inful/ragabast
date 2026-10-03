package service

import (
	"context"
	"fmt"
	"maps"
	"strings"

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
	// Filters narrow the retrieval that builds the LLM's
	// context. Empty (zero-value SearchFilters) means "no
	// filter / all sources", matching the post-filter
	// semantics in the vector layer. The chat handler uses
	// this to scope a query to a subset of source kinds
	// (e.g. only GitLab issues).
	Filters SearchFilters
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
	// Post-filter by source kind. The chat handler can scope a
	// retrieval to one or more source kinds (e.g. "only GitLab
	// issues") via SearchFilters.SourceKinds; this is the
	// cheapest place to drop out-of-scope results because the
	// top-K is small by the time we get here. See
	// applySourceKindFilters.
	results = applySourceKindFilters(results, filters)
	s.enrichWithDocbuilderURLs(results)
	s.enrichWithCitationURLs(results)
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
	// Post-filter by source kind. Same code path as Search;
	// applies regardless of mode.
	results = applySourceKindFilters(results, filters)
	s.enrichWithDocbuilderURLs(results)
	s.enrichWithCitationURLs(results)
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

// enrichWithCitationURLs populates the CitationURL field on
// every result using the per-source-kind dispatch
// (SourceLinkURL). The chat sources-panel template and the
// search-results page both consume CitationURL instead of
// branching on DocbuilderURL vs DocumentURLs themselves —
// keeping the dispatch logic in Go (service layer) and out of
// the templates, where it would be untestable. Idempotent:
// safe to call alongside enrichWithDocbuilderURLs.
//
// Differs from enrichWithDocbuilderURLs in that it does NOT
// short-circuit on the absence of ragabast.docbuilder_base_url:
// the dispatch can still produce a URL from DocumentURLs
// alone (the gitlab case), so a result with no DocbuilderURL
// still gets a populated CitationURL when DocumentURLs is
// non-empty.
func (s *Service) enrichWithCitationURLs(results []models.SearchResult) {
	for i := range results {
		results[i].CitationURL = SourceLinkURL(results[i])
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

// Query performs a search and generates a natural language response using LLM.
//
// The README documents Service.Search + Service.FindDocuments
// as the find-docs replacement, but FindDocuments is not yet
// implemented and the chat handler still calls this method on
// every POST /chat/message. Issue #89 used to emit a one-shot
// runtime deprecation here; that warning was removed because it
// pointed at a method that did not exist, which made the message
// misleading. Re-add a deprecation once FindDocuments lands.
func (s *Service) Query(ctx context.Context, query string, limit int) (string, error) {
	response, _, err := s.QueryDebugWithOptions(ctx, query, limit, LLMOptions{})
	return response, err
}

// QueryDebugWithOptions performs a search and generates a response, returning
// debug information about the retrieved chunks and constructed prompt.
//
// See the note on Service.Query about the deprecation timeline.
func (s *Service) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts LLMOptions) (string, *QueryDebugInfo, error) {
	results, err := s.vectorOps.Search(ctx, query, limit, opts.Filters.ToWhere())
	if err != nil {
		return "", nil, fmt.Errorf("search failed: %w", err)
	}
	// Post-filter by source kind. Mirrors the post-filter in
	// Search and HybridSearch: the chromem-go Where filter is
	// equality-only, so per-kind scoping happens here against
	// the kind populated on each chunk at read time (see
	// inferSourceKind in the vector package).
	results = applySourceKindFilters(results, opts.Filters)

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
	// Populate CitationURL with the per-source-kind URL choice
	// (DocumentURLs[0] for gitlab, DocbuilderURL for docbuilder).
	// The chat sources panel and the search-results page both
	// consume this so the dispatch logic stays out of the
	// templates.
	s.enrichWithCitationURLs(results)

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
