package service

import (
	"context"
	"fmt"
	"log"
	"maps"
	"strings"
	"sync"

	"github.com/ragabast/internal/models"
)

// SearchFilters narrows a Search by document-level attributes.
// Empty fields are ignored. Where multiple fields are set, they
// combine as AND across distinct metadata keys.
//
// Filters are translated to chromem-go's map[string]string Where
// filter at the service boundary:
//
//   - DocumentID -> "document_id"  (exact)
//   - Tag        -> "document_tags"  (substring against the
//     "\n"-joined tag list)
//   - Category   -> "document_categories"
//
// Substring matching is intentional: chunk-level metadata stores
// tags and categories as "\n"-joined values, and a single
// substring match is what chromem-go can deliver without
// round-tripping per chunk.
type SearchFilters struct {
	DocumentID string
	Tag        string
	Category   string
}

// toWhere converts the struct into the chromem-go Where filter.
// Returns a fresh map; the caller may mutate it freely.
func (f SearchFilters) toWhere() map[string]string {
	where := map[string]string{}
	if d := strings.TrimSpace(f.DocumentID); d != "" {
		where["document_id"] = d
	}
	if t := strings.TrimSpace(f.Tag); t != "" {
		where["document_tags"] = t
	}
	if c := strings.TrimSpace(f.Category); c != "" {
		where["document_categories"] = c
	}
	return where
}

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
func (s *Service) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]models.SearchResult, error) {
	results, err := s.vectorOps.Search(ctx, query, limit, filters.toWhere())
	if err != nil {
		return nil, wrapCorruptionError(err)
	}
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

// deprecatedChatOnce guards the warning so it fires at most
// once per method name across the whole process. Without this
// the log would spam on every request. Adding a new chat-mode
// method? Just call warnDeprecated with the method name.
var deprecatedChatOnce sync.Map // map[string]*sync.Once

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

// QueryDebug performs a search and generates a response, returning debug information
// about the retrieved chunks and constructed prompt.
//
// Deprecated: see Service.Query.
func (s *Service) QueryDebug(ctx context.Context, query string, limit int) (string, *QueryDebugInfo, error) {
	warnDeprecated("QueryDebug")
	return s.QueryDebugWithOptions(ctx, query, limit, LLMOptions{})
}

// QueryDebugWithOptions is like QueryDebug but allows controlling LLM generation options.
//
// Deprecated: see Service.Query.
func (s *Service) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts LLMOptions) (string, *QueryDebugInfo, error) {
	warnDeprecated("QueryDebugWithOptions")
	results, err := s.vectorOps.Search(ctx, query, limit, nil)
	if err != nil {
		return "", nil, fmt.Errorf("search failed: %w", err)
	}

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
