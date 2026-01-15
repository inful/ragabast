package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

type fakeChatService struct {
	answer string
}

func (f *fakeChatService) CheckHealth(ctx context.Context) (bool, error) {
	return true, nil
}

func (f *fakeChatService) IngestDocument(ctx context.Context, content string) (*models.Document, error) {
	return &models.Document{ID: "doc-1"}, nil
}

func (f *fakeChatService) Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error) {
	return nil, nil
}

func (f *fakeChatService) ListDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	return nil, nil
}

func (f *fakeChatService) DeleteDocument(ctx context.Context, documentID string) error {
	return nil
}

func (f *fakeChatService) SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (service.FrontmatterSuggestion, error) {
	return service.FrontmatterSuggestion{}, nil
}

func (f *fakeChatService) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error) {
	return f.answer, &service.QueryDebugInfo{Results: []models.SearchResult{}}, nil
}

func (f *fakeChatService) QueryWithLLM(ctx context.Context, query string, model string, history []struct {
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

func (f *fakeChatService) GetNormalizedTags(ctx context.Context) ([]string, error) {
	return []string{"go", "rag", "api"}, nil
}

func (f *fakeChatService) GetNormalizedCategories(ctx context.Context) ([]string, error) {
	return []string{"Guides", "Reference", "Tutorials"}, nil
}

func (f *fakeChatService) GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error) {
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

func TestChatPage_RendersHTMXForm(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeChatService{answer: "ok"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "hx-post=\"/chat/message\"")
	require.Contains(t, w.Body.String(), "id=\"chat-messages\"")
}

func TestChatMessage_AppendsUserAndAssistant(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeChatService{answer: "See https://example.com/a?x=1&y=2"})

	form := url.Values{}
	form.Set("message", "<b>hi</b>")

	req := httptest.NewRequest(http.MethodPost, "/chat/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	require.Contains(t, body, "hx-swap-oob=\"delete\"")
	require.Contains(t, body, "chat-messages-placeholder")
	require.Contains(t, body, "You")
	require.Contains(t, body, "Assistant")
	require.Contains(t, body, "&lt;b&gt;hi&lt;/b&gt;")
	require.Contains(t, body, "href=\"https://example.com/a?x=1&amp;y=2\"")
	require.Contains(t, body, "target=\"_blank\"")
	require.Contains(t, body, "noopener noreferrer")
	require.Contains(t, body, "https://example.com/a?x=1&amp;y=2")
}
