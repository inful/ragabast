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
type searchRequestBody struct {
	Query    string  `doc:"Search query" json:"query"`
	Limit    int     `default:"5" doc:"Number of results" json:"limit" minimum:"1"`
	MinScore float64 `default:"0.5" doc:"Minimum similarity score" json:"min_score" maximum:"1" minimum:"0"`
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
		Summary:     "Semantic search",
	}, func(ctx context.Context, input *struct{ Body searchRequestBody }) (*struct{ Body searchResponseBody }, error) {
		q := strings.TrimSpace(input.Body.Query)
		if q == "" {
			return nil, huma.Error400BadRequest("query is required")
		}

		limit := input.Body.Limit
		if limit == 0 {
			limit = 5
		}
		minScore := input.Body.MinScore
		if minScore == 0 {
			minScore = 0.5
		}

		results, err := svc.Search(ctx, q, limit, service.SearchFilters{})
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
