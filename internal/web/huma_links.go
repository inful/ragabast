package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/models"
)

type linkSuggestionsRequestBody struct {
	Text string `doc:"A section of documentation used to find related documents" json:"text"`

	TopK     *int     `default:"10" doc:"Number of search hits to consider" json:"top_k,omitempty" minimum:"1"`
	MaxURLs  *int     `default:"10" doc:"Maximum number of URLs to return" json:"max_urls,omitempty" minimum:"1"`
	MinScore *float64 `default:"0.5" doc:"Minimum similarity score" json:"min_score,omitempty" maximum:"1" minimum:"0"`
}

type linkSuggestionsResponseBody struct {
	URLs []string `json:"urls"`
}

// registerLinkSuggestionsOperation wires POST /api/link-suggestions.
func registerLinkSuggestionsOperation(api huma.API, svc serviceAPI) {
	huma.Register(api, huma.Operation{
		OperationID: "link-suggestions",
		Method:      http.MethodPost,
		Path:        "/api/link-suggestions",
		Summary:     "Suggest related document links",
		Description: "Given a section of documentation, search the vector database and return a set of related document URLs.",
	}, func(ctx context.Context, input *struct{ Body linkSuggestionsRequestBody }) (*struct{ Body linkSuggestionsResponseBody }, error) {
		text := strings.TrimSpace(input.Body.Text)
		if text == "" {
			return nil, huma.Error400BadRequest("text is required")
		}

		topK := 10
		if input.Body.TopK != nil {
			topK = *input.Body.TopK
		}
		if topK == 0 {
			topK = 10
		}
		maxURLs := 10
		if input.Body.MaxURLs != nil {
			maxURLs = *input.Body.MaxURLs
		}
		if maxURLs == 0 {
			maxURLs = 10
		}
		minScore := 0.5
		if input.Body.MinScore != nil {
			minScore = *input.Body.MinScore
		}
		if minScore == 0 {
			minScore = 0.5
		}

		results, err := svc.Search(ctx, text, topK, nil)
		if err != nil {
			return nil, huma.Error500InternalServerError("link suggestions search failed")
		}

		filtered := make([]models.SearchResult, 0, len(results))
		for _, r := range results {
			if float64(r.Similarity) < minScore {
				continue
			}
			filtered = append(filtered, r)
		}

		urls := extractLinksFromResults(filtered)
		if len(urls) > maxURLs {
			urls = urls[:maxURLs]
		}

		return &struct{ Body linkSuggestionsResponseBody }{Body: linkSuggestionsResponseBody{URLs: urls}}, nil
	})
}
