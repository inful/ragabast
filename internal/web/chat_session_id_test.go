package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// buildChatForm returns the minimum form body the chat
// handler accepts: csrf_token + message. Used by the
// session-id wiring tests so each test focuses on the
// session-id plumbing without re-typing boilerplate.
func buildChatForm(message string) url.Values {
	f := url.Values{}
	f.Set("csrf_token", "test-token")
	f.Set("message", message)
	return f
}

// TestHandleChatMessage_ThreadsHistory pins the issue
// #22 wiring contract: prior history returned by the
// service is placed into LLMOptions.History before the
// LLM is invoked. Without this, follow-up questions
// ("tell me more about that") would never see prior
// context, regardless of what the service stores.
func TestHandleChatMessage_ThreadsHistory(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeHumaService{
		answer: "answer-2",
		history: []service.ChatMessage{
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "first answer"},
		},
	}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message",
		strings.NewReader(buildChatForm("follow-up").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, svc.lastQueryOpts.History, 2,
		"chat handler must thread prior history into the LLM call")
	require.Equal(t, "first question", svc.lastQueryOpts.History[0].Content)
	require.Equal(t, "first answer", svc.lastQueryOpts.History[1].Content)
}

// TestHandleChatMessage_AppendsExchange pins the other
// half of issue #22: after a successful reply, the
// handler asks the service to remember the exchange.
// Without this, the next request in the same session
// would still see the OLD history — the user would have
// to refresh, restart, or relog to make any progress.
func TestHandleChatMessage_AppendsExchange(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeHumaService{answer: "answer text"}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message",
		strings.NewReader(buildChatFormWithSession("hi", "sess-1").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, svc.appendedTurns, 1,
		"chat handler must remember the exchange for the next request")
	require.Equal(t, "sess-1", svc.appendedTurns[0].sessionID)
	require.Len(t, svc.appendedTurns[0].messages, 2,
		"exchange must include BOTH user + assistant messages")
	require.Equal(t, "user", svc.appendedTurns[0].messages[0].Role)
	require.Equal(t, "hi", svc.appendedTurns[0].messages[0].Content)
	require.Equal(t, "assistant", svc.appendedTurns[0].messages[1].Role)
	require.Equal(t, "answer text", svc.appendedTurns[0].messages[1].Content)
}

// TestHandleChatMessage_EmptySessionID_StaysStateless
// pins the opt-out path: callers who don't pass a
// session_id (legacy form, programmatic use, or a
// private-mode toggle that clears it) still get a reply,
// and no session is created. The previous behavior of
// "every chat is stateless" must still work.
func TestHandleChatMessage_EmptySessionID_StaysStateless(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeHumaService{answer: "ok"}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message",
		strings.NewReader(buildChatForm("ping").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, svc.lastQueryOpts.History,
		"no session_id → no prior history surfaced to the LLM")
	require.Empty(t, svc.appendedTurns,
		"no session_id → handler must NOT remember this exchange")
}

// TestHandleChatMessage_QueryError_DoesNotRemember
// pins the failure path: if the LLM call errors out,
// the exchange is NOT appended. Without this, every
// error would corrupt the session history with half-
// baked exchanges that the LLM would later see and
// hallucinate around.
func TestHandleChatMessage_QueryError_DoesNotRemember(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeHumaService{queryErr: errFakeUnhealthy}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message",
		strings.NewReader(buildChatFormWithSession("hello", "sess-err").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Empty(t, svc.appendedTurns,
		"on error, the handler must NOT remember a partial exchange")
}

// TestHandleChatPage_GeneratesSessionIDCookie pins the
// first-time-visitor flow: GET / without an existing
// cookie sets a fresh ragabast_chat_session cookie so
// reloads persist across the same browser session.
func TestHandleChatPage_GeneratesSessionIDCookie(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	cookies := rec.Result().Cookies()
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == chatSessionCookieName {
			session = c
			break
		}
	}
	require.NotNil(t, session,
		"first-time visitor must get a chat session cookie so reloads preserve context")
	require.NotEmpty(t, session.Value)
	require.True(t, session.HttpOnly,
		"session cookie must be HttpOnly — JS doesn't need to read it")
	require.Equal(t, http.SameSiteLaxMode, session.SameSite,
		"top-level navigations must work; cross-site form POSTs must not")
}

// TestHandleChatPage_ReusesExistingCookie pins the
// reload flow: GET / with an existing cookie reuses the
// same id (no churn). Without this, every reload would
// create a new session and lose continuity — defeating
// the entire purpose of the feature.
func TestHandleChatPage_ReusesExistingCookie(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: chatSessionCookieName, Value: "stable-id"})
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	for _, c := range rec.Result().Cookies() {
		require.NotEqual(t, chatSessionCookieName, c.Name,
			"existing cookie must NOT be overwritten — reloads must keep the same id")
	}
}

// TestHandleChatClear_DropsSessionAndCookie pins the
// privacy affordance: POST /chat/clear drops both the
// server-side history AND the cookie. This is the
// "private mode" button — the next chat must start
// clean.
func TestHandleChatClear_DropsSessionAndCookie(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("csrf_token", "test-token")
	form.Set("session_id", "old-session")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/clear",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/", rec.Header().Get("Location"),
		"clear must redirect back to the chat page so a fresh cookie is set")
	require.Equal(t, []string{"old-session"}, svc.clearedSessions,
		"clear must ask the service to drop the session")

	// Cookie should be cleared (MaxAge = -1).
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == chatSessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "clear must issue a Set-Cookie to drop the browser cookie")
	require.Equal(t, -1, sessionCookie.MaxAge,
		"MaxAge=-1 is the spec'd way to tell the browser to delete a cookie")
}

// buildChatFormWithSession is the chat form with a
// session_id field — used by the issue #22 wiring tests.
func buildChatFormWithSession(message, sessionID string) url.Values {
	f := buildChatForm(message)
	f.Set("session_id", sessionID)
	return f
}
