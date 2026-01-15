package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

type fakeService struct {
	searchResults []models.SearchResult

	queryAnswer string
	queryDebug  *service.QueryDebugInfo
}

func (f *fakeService) CheckHealth(ctx context.Context) (bool, error) {
	return true, nil
}

func (f *fakeService) IngestDocument(ctx context.Context, content string) (*models.Document, error) {
	return &models.Document{ID: "doc-1"}, nil
}

func (f *fakeService) Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error) {
	return f.searchResults, nil
}

func (f *fakeService) ListDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	return []models.DocumentInfo{}, nil
}

func (f *fakeService) DeleteDocument(ctx context.Context, documentID string) error {
	return nil
}

func (f *fakeService) SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (service.FrontmatterSuggestion, error) {
	return service.FrontmatterSuggestion{}, nil
}

func (f *fakeService) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error) {
	if f.queryDebug == nil {
		f.queryDebug = &service.QueryDebugInfo{Results: []models.SearchResult{}}
	}
	return f.queryAnswer, f.queryDebug, nil
}

func (f *fakeService) QueryWithLLM(ctx context.Context, query string, model string, history []struct {
	Role    string
	Content string
}) (string, []struct {
	ID      string
	Content string
	Score   float64
}, error,
) {
	return "", nil, nil
}

func (f *fakeService) GetNormalizedTags(ctx context.Context) ([]string, error) {
	return []string{"go", "rag", "api"}, nil
}

func (f *fakeService) GetNormalizedCategories(ctx context.Context) ([]string, error) {
	return []string{"Guides", "Reference", "Tutorials"}, nil
}

func (f *fakeService) GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error) {
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

func TestHandleSearchAPI_FiltersByMinScore(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{ChunkID: "c1", DocumentID: "d1", Content: "hi", DocumentTitle: "Doc", Similarity: 0.6},
			{ChunkID: "c2", DocumentID: "d2", Content: "bye", DocumentTitle: "Doc2", Similarity: 0.4},
		},
	}

	s := NewServer(cfg, svc)

	body := []byte(`{"query":"q","limit":5,"min_score":0.5}`)
	req := httptest.NewRequest(http.MethodPost, "/api/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Results []map[string]any `json:"results"`
		Count   int              `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 1, resp.Count)
	require.Len(t, resp.Results, 1)
	require.Equal(t, "c1", resp.Results[0]["chunk_id"])
}

func TestHandleQueryAPI_PassesModelAndHistory(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		queryAnswer: "answer",
		queryDebug: &service.QueryDebugInfo{Results: []models.SearchResult{
			{ChunkID: "c1", Content: "ctx", Similarity: 0.9, DocumentURLs: []string{"https://example.com/a"}},
		}},
	}

	s := NewServer(cfg, svc)

	body := []byte(`{"query":"q","top_k":5,"include_hits":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Answer string   `json:"answer"`
		Links  []string `json:"links"`
		Hits   []any    `json:"hits"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "answer", resp.Answer)
	require.Contains(t, resp.Links, "https://example.com/a")
	require.Len(t, resp.Hits, 1)
}
