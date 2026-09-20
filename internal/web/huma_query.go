package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/service"
)

// queryRequestBody is the JSON shape POSTed to /api/query.
type queryRequestBody struct {
	Query       string   `doc:"Natural language query" json:"query"`
	TopK        int      `default:"5" doc:"Number of results to consider" json:"top_k" maximum:"50" minimum:"1"`
	Temperature *float64 `doc:"LLM temperature (sampling). If omitted, uses model default." json:"temperature,omitempty"`
	IncludeHits bool     `doc:"Include retrieved chunks in the response." json:"include_hits,omitempty"`
	History     []struct {
		Role    string `doc:"Message role (user|assistant)." json:"role"`
		Content string `doc:"Message content." json:"content"`
	} `doc:"Optional conversation history for follow-up questions." json:"history,omitempty"`
}

// queryResponseBody is the JSON shape returned from /api/query.
type queryResponseBody struct {
	Answer string     `json:"answer"`
	Links  []string   `json:"links,omitempty"`
	Hits   []queryHit `json:"hits,omitempty"`
}

// queryHit is one retrieved chunk, optionally surfaced to the
// client when IncludeHits is true.
type queryHit struct {
	DocumentTitle string   `json:"document_title"`
	Content       string   `json:"content"`
	Similarity    float32  `json:"similarity"`
	DocumentURLs  []string `json:"document_urls,omitempty"`
}

// searchRequestBody is the JSON shape POSTed to /api/search.
//
// Mode defaults to "hybrid" (v0.4.0). Pass "semantic" for
// pure embedding similarity (the v0.3.0 behavior) or
// "keyword" for bleve-only search. The three modes are
// useful for callers debugging why a particular chunk ranks
// where it does.
//
// The document_id / tag / category filters are applied to
// BOTH rankings before fusion; a chunk that fails any filter
// never appears in the result.
//
// min_score is a pointer so the field is genuinely optional:
// when omitted (nil), no minimum-relevance filter applies.
// A v0.3.0 caller that omits min_score would have seen a
// default of 0.5 applied server-side; the v0.4.0 default
// is 0, which keeps low-similarity results in the response
// for callers who want to see everything the ranking
// produced.
type searchRequestBody struct {
	Query      string   `doc:"Search query" json:"query"`
	Limit      int      `default:"5" doc:"Number of results" json:"limit" minimum:"1"`
	MinScore   *float64 `doc:"Minimum relevance score in [0,1]. Omit for no minimum." json:"min_score,omitempty" maximum:"1" minimum:"0"`
	Mode       string   `doc:"Search mode: hybrid (default), semantic, or keyword" enum:"hybrid,semantic,keyword" json:"mode,omitempty"`
	DocumentID string   `doc:"Restrict results to one document" json:"document_id,omitempty"`
	Tag        string   `doc:"Restrict results to chunks whose document has this tag" json:"tag,omitempty"`
	Category   string   `doc:"Restrict results to chunks whose document has this category" json:"category,omitempty"`
}

type searchResultBody struct {
	ChunkID       string   `json:"chunk_id"`
	DocumentID    string   `json:"document_id"`
	DocumentTitle string   `json:"document_title"`
	Content       string   `json:"content"`
	Similarity    float32  `json:"similarity"`
	DocumentURLs  []string `json:"document_urls,omitempty"`
}

type searchResponseBody struct {
	Count   int                `json:"count"`
	Results []searchResultBody `json:"results"`
}

// registerQueryOperation wires /api/query.
func registerQueryOperation(api huma.API, svc serviceAPI) {
	huma.Register(api, huma.Operation{
		OperationID: "query",
		Method:      http.MethodPost,
		Path:        "/api/query",
		Summary:     "Query the knowledge base",
	}, func(ctx context.Context, input *struct{ Body queryRequestBody }) (*struct{ Body queryResponseBody }, error) {
		q := strings.TrimSpace(input.Body.Query)
		if q == "" {
			return nil, huma.Error400BadRequest("query is required")
		}

		topK := input.Body.TopK
		if topK == 0 {
			topK = 5
		}

		history := make([]service.ChatMessage, 0, len(input.Body.History))
		for _, m := range input.Body.History {
			history = append(history, service.ChatMessage{Role: m.Role, Content: m.Content})
		}

		answer, debug, err := svc.QueryDebugWithOptions(ctx, q, topK, service.LLMOptions{Temperature: input.Body.Temperature, History: history})
		if err != nil {
			return nil, huma.Error500InternalServerError("query failed")
		}

		resp := queryResponseBody{Answer: answer}
		if debug != nil {
			resp.Links = extractLinksFromResults(debug.Results)
			if input.Body.IncludeHits {
				resp.Hits = make([]queryHit, 0, len(debug.Results))
				for _, r := range debug.Results {
					resp.Hits = append(resp.Hits, queryHit{
						DocumentTitle: r.DocumentTitle,
						Content:       r.Content,
						Similarity:    r.Similarity,
						DocumentURLs:  r.DocumentURLs,
					})
				}
			}
		}

		return &struct{ Body queryResponseBody }{Body: resp}, nil
	})
}

// registerSearchOperation wires /api/search.
func registerSearchOperation(api huma.API, svc serviceAPI) {
	huma.Register(api, huma.Operation{
		OperationID: "search",
		Method:      http.MethodPost,
		Path:        "/api/search",
		Summary:     "Hybrid search across the embedding index and the keyword index",
	}, func(ctx context.Context, input *struct{ Body searchRequestBody }) (*struct{ Body searchResponseBody }, error) {
		q := strings.TrimSpace(input.Body.Query)
		if q == "" {
			return nil, huma.Error400BadRequest("query is required")
		}

		limit := input.Body.Limit
		if limit == 0 {
			limit = 5
		}
		// min_score default dropped from 0.5 to 0 in v0.4.0:
		// semantic similarity scores vary with the embeddings
		// model and 0.5 was filtering too aggressively for
		// nomic-embed-text. Callers who want the old
		// behavior can pass min_score: 0.5 explicitly.
		// The pointer is nil when the caller omits the field;
		// dereference safely.
		var minScore float64
		if input.Body.MinScore != nil {
			minScore = *input.Body.MinScore
		}

		mode := parseSearchMode(input.Body.Mode)
		filters := service.SearchFilters{
			DocumentID: input.Body.DocumentID,
			Tag:        input.Body.Tag,
			Category:   input.Body.Category,
		}

		results, err := svc.HybridSearch(ctx, q, limit, filters, mode)
		if err != nil {
			return nil, huma.Error500InternalServerError("search failed")
		}

		filtered := make([]searchResultBody, 0, len(results))
		for _, r := range results {
			if float64(r.Similarity) < minScore {
				continue
			}
			filtered = append(filtered, searchResultBody{
				ChunkID:       r.ChunkID,
				DocumentID:    r.DocumentID,
				DocumentTitle: r.DocumentTitle,
				Content:       r.Content,
				Similarity:    r.Similarity,
				DocumentURLs:  r.DocumentURLs,
			})
		}

		return &struct{ Body searchResponseBody }{Body: searchResponseBody{Count: len(filtered), Results: filtered}}, nil
	})
}

// parseSearchMode maps the JSON wire string to the
// service.SearchMode enum. Defaults to ModeHybrid when
// the caller omits the field — this is the v0.4.0
// behavior change from the v0.3.0 pure-semantic default.
func parseSearchMode(s string) service.SearchMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "semantic":
		return service.ModeSemantic
	case "keyword":
		return service.ModeKeyword
	case "", "hybrid":
		return service.ModeHybrid
	default:
		// Unknown mode — fall back to hybrid. We could
		// 400 here but the hybrid path is the safest
		// default and the error path is reserved for
		// real failures (corrupt index, embeddings
		// server down).
		return service.ModeHybrid
	}
}
