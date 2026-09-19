package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

func TestChatPage_RendersHTMXForm(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{queryAnswer: "ok"})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "hx-post=\"/chat/message\"")
	require.Contains(t, w.Body.String(), "id=\"chat-messages\"")
}

func TestChatMessage_AppendsUserAndAssistant(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{queryAnswer: "See https://example.com/a?x=1&y=2"})

	form := url.Values{}
	form.Set("message", "<b>hi</b>")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form.Encode()))
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

// TestChatMessage_ServiceErrorReturnsGeneric500 ensures the server does not
// leak internal error details (model names, server URLs, stack traces) to the
// client. The full error is logged server-side.
func TestChatMessage_ServiceErrorReturnsGeneric500(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{queryErr: errors.New("embeddings API returned status 401: API key required")}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("message", "hello")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, "Internal server error\n", w.Body.String())
	require.NotContains(t, w.Body.String(), "API key")
	require.NotContains(t, w.Body.String(), "401")
}
