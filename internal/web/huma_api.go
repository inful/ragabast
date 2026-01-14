package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

type queryRequestBody struct {
	Query       string   `doc:"Natural language query" json:"query"`
	TopK        int      `default:"5" doc:"Number of results to consider" json:"top_k" minimum:"1"`
	Temperature *float64 `doc:"LLM temperature (sampling). If omitted, uses model default." json:"temperature,omitempty"`
	IncludeHits bool     `doc:"Include retrieved chunks in the response." json:"include_hits,omitempty"`
	History     []struct {
		Role    string `doc:"Message role (user|assistant)." json:"role"`
		Content string `doc:"Message content." json:"content"`
	} `doc:"Optional conversation history for follow-up questions." json:"history,omitempty"`
}

type queryResponseBody struct {
	Answer string     `json:"answer"`
	Links  []string   `json:"links,omitempty"`
	Hits   []queryHit `json:"hits,omitempty"`
}

type queryHit struct {
	DocumentTitle string   `json:"document_title"`
	Content       string   `json:"content"`
	Similarity    float32  `json:"similarity"`
	DocumentURLs  []string `json:"document_urls,omitempty"`
}

type ingestRequestBody struct {
	Content string `doc:"Docubilder document content (including YAML frontmatter)." json:"content"`
}

type ingestResponseBody struct {
	Message    string `json:"message"`
	DocumentID string `json:"document_id"`
	Chunks     int    `json:"chunks"`
}

type healthResponseBody struct {
	Status string `json:"status"`
}

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

type documentsResponseBody struct {
	Documents []models.DocumentInfo `json:"documents"`
}

func RegisterHumaOperations(api huma.API, svc serviceAPI) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
		Summary:     "Health check",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body healthResponseBody }, error) {
		_, err := svc.CheckHealth(ctx)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("Service unhealthy")
		}
		return &struct{ Body healthResponseBody }{Body: healthResponseBody{Status: "healthy"}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest",
		Method:      http.MethodPost,
		Path:        "/api/ingest",
		Summary:     "Ingest a docubilder document",
	}, func(ctx context.Context, input *struct{ Body ingestRequestBody }) (*struct{ Body ingestResponseBody }, error) {
		content := strings.TrimSpace(input.Body.Content)
		if content == "" {
			return nil, huma.Error400BadRequest("content is required")
		}

		doc, err := svc.IngestDocument(ctx, content)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-raw",
		Method:      http.MethodPost,
		Path:        "/api/ingest/raw",
		Summary:     "Ingest a docubilder markdown document (raw body)",
	}, func(ctx context.Context, input *struct {
		RawBody []byte `contentType:"text/markdown" required:"true"`
	},
	) (*struct{ Body ingestResponseBody }, error) {
		if len(bytes.TrimSpace(input.RawBody)) == 0 {
			return nil, huma.Error400BadRequest("request body is required")
		}
		content := string(input.RawBody)

		doc, err := svc.IngestDocument(ctx, content)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-file",
		Method:      http.MethodPost,
		Path:        "/api/ingest/file",
		Summary:     "Ingest a docubilder markdown document (multipart upload)",
	}, func(ctx context.Context, input *struct {
		RawBody huma.MultipartFormFiles[struct {
			File huma.FormFile `form:"file" required:"true"`
		}]
	},
	) (*struct{ Body ingestResponseBody }, error) {
		form := input.RawBody.Data()
		b, err := io.ReadAll(form.File)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to read uploaded file")
		}

		if len(bytes.TrimSpace(b)) == 0 {
			return nil, huma.Error400BadRequest("file is empty")
		}
		content := string(b)

		doc, err := svc.IngestDocument(ctx, content)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})

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

		results, err := svc.Search(ctx, q, limit, nil)
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

	huma.Register(api, huma.Operation{
		OperationID: "documents",
		Method:      http.MethodGet,
		Path:        "/api/documents",
		Summary:     "List ingested documents",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body documentsResponseBody }, error) {
		docs, err := svc.ListDocuments(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list documents failed")
		}
		return &struct{ Body documentsResponseBody }{Body: documentsResponseBody{Documents: docs}}, nil
	})
}

func extractLinksFromResults(results []models.SearchResult) []string {
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, r := range results {
		for _, u := range r.DocumentURLs {
			url := strings.TrimSpace(u)
			if url == "" {
				continue
			}
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			out = append(out, url)
		}
	}
	return out
}
