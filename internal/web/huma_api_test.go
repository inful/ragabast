package web

import (
	"context"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

type fakeHumaService struct {
	healthErr error
	ingested  *models.Document
	ingestErr error
	answer    string
	debug     *service.QueryDebugInfo
	queryErr  error
}

func (f *fakeHumaService) CheckHealth(ctx context.Context) (bool, error) {
	return f.healthErr == nil, f.healthErr
}

func (f *fakeHumaService) IngestDocument(ctx context.Context, content string) (*models.Document, error) {
	if f.ingestErr != nil {
		return nil, f.ingestErr
	}
	if f.ingested != nil {
		return f.ingested, nil
	}
	return &models.Document{ID: "doc-1", Chunks: []models.Chunk{{}, {}}}, nil
}

func (f *fakeHumaService) Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error) {
	return nil, nil
}

func (f *fakeHumaService) ListDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	return nil, nil
}

func (f *fakeHumaService) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error) {
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

func (f *fakeHumaService) QueryWithLLM(ctx context.Context, query string, model string, history []struct {
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

func TestHumaAPI_Health(t *testing.T) {
	_, api := humatest.New(t)
	RegisterHumaOperations(api, &fakeHumaService{})

	w := api.Get("/api/health")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "healthy")
}

func TestHumaAPI_Ingest_ValidatesContent(t *testing.T) {
	_, api := humatest.New(t)
	RegisterHumaOperations(api, &fakeHumaService{})

	w := api.Post("/api/ingest", map[string]any{"content": ""})
	require.Equal(t, 400, w.Code)
}

func TestHumaAPI_Query_ReturnsLinks(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{debug: &service.QueryDebugInfo{Results: []models.SearchResult{{DocumentURLs: []string{"https://example.com/a"}}}}}
	RegisterHumaOperations(api, svc)

	w := api.Post("/api/query", map[string]any{"query": "what is ragabast?", "top_k": 5})
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "Links")
	require.Contains(t, w.Body.String(), "https://example.com/a")
}
