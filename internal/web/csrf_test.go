package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCsrfMiddleware_FirstGetSetsCookie pins the basic
// double-submit-cookie handshake: a fresh client has no
// csrf_token cookie. The first GET to a public route must
// generate one and set it as `ragabast_csrf`. Without this
// step, a client could never POST a valid form because
// the form has no token to echo.
func TestCsrfMiddleware_FirstGetSetsCookie(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	cookies := w.Result().Cookies()
	var csrf *http.Cookie
	for _, c := range cookies {
		if c.Name == csrfCookieName {
			csrf = c
			break
		}
	}
	require.NotNil(t, csrf, "first GET must set the csrf cookie")
	assert.NotEmpty(t, csrf.Value, "csrf cookie value must be non-empty")
	assert.True(t, csrf.HttpOnly, "csrf cookie must be HttpOnly (server-side rendering means JS doesn't need to read it)")
	assert.Equal(t, "/", csrf.Path)
	assert.Equal(t, http.SameSiteLaxMode, csrf.SameSite,
		"csrf cookie must be SameSite=Lax so it survives top-level navigations")
}

// TestCsrfMiddleware_SubsequentGetReusesCookie pins
// the second half of the handshake: once a client has a
// csrf cookie, the server must NOT replace it on a later
// GET. Otherwise an attacker who could force a navigation
// could rotate the victim's token mid-session.
func TestCsrfMiddleware_SubsequentGetReusesCookie(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	const existing = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	req1 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req1.AddCookie(&http.Cookie{Name: csrfCookieName, Value: existing})
	w1 := httptest.NewRecorder()
	s.router.ServeHTTP(w1, req1)

	cookies := w1.Result().Cookies()
	for _, c := range cookies {
		if c.Name == csrfCookieName {
			assert.Equal(t, existing, c.Value,
				"server must not rotate the csrf cookie on a subsequent GET")
			return
		}
	}
	// No Set-Cookie at all is also acceptable — but if one
	// was set it must NOT replace the existing token.
}

// TestCsrfMiddleware_PostWithoutToken_Forbidden covers
// the canonical attack: a cross-origin attacker POSTs to
// /chat/message from a third-party page. The browser
// doesn't send the csrf_token cookie (because the
// attacker can't read it cross-origin) AND can't put a
// matching csrf_token form field in place. The server
// must reject with 403.
func TestCsrfMiddleware_PostWithoutToken_Forbidden(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	form := url.Values{"message": {"hi"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"POST without csrf_token must be 403 (got %d)", w.Code)
}

// TestCsrfMiddleware_PostWithMismatchedToken_Forbidden
// covers the partial-cookie scenario: an attacker has
// SOME csrf cookie (maybe a stale one from a previous
// session) but no matching field value to put in the
// form. Server must reject.
func TestCsrfMiddleware_PostWithMismatchedToken_Forbidden(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	form := url.Values{
		"message":    {"hi"},
		"csrf_token": {"attacker-guess"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "real-token"})
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"POST with mismatched csrf_token must be 403 (got %d)", w.Code)
}

// TestCsrfMiddleware_PostWithMatchingToken_Accepted
// pins the happy path: the form was rendered with the
// correct token, the browser included the cookie, and the
// hidden field echoes the same value. Server accepts.
func TestCsrfMiddleware_PostWithMatchingToken_Accepted(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	const token = "test-csrf-token-must-match-32bytes-hex"
	form := url.Values{
		"message":    {"hi"},
		"csrf_token": {token},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	// 200 from the chat handler — not 401/403.
	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"matching csrf_token must NOT 403 (got %d)", w.Code)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code,
		"matching csrf_token with valid bearer must NOT 401 (got %d)", w.Code)
}

// TestCsrfMiddleware_BearerAuthBypassesCsrf pins the
// "API clients don't need CSRF" carve-out: requests with
// `Authorization: Bearer <token>` cannot be made
// cross-origin by a browser (browsers don't send
// Authorization headers cross-origin), so CSRF is
// irrelevant. The check is skipped entirely so script
// clients and curl don't have to manage csrf cookies.
func TestCsrfMiddleware_BearerAuthBypassesCsrf(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	body := `{"message":"hi"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/chat",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	// NO csrf cookie. NO csrf field. Bearer alone is enough.
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"Bearer-auth POST without csrf_token must NOT 403 (got %d)", w.Code)
}

// TestChatForm_RendersCsrfTokenField pins the
// server-side-render contract: every form that POSTs
// to a CSRF-protected endpoint must include a hidden
// csrf_token input. Without this, no human user can
// ever submit the form. The browser can't read the
// HttpOnly cookie, so the value MUST be rendered by
// the server, not by client-side JavaScript.
func TestChatForm_RendersCsrfTokenField(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/chat", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	assert.Contains(t, body, `name="csrf_token"`,
		"chat form must render <input name=\"csrf_token\">")
	assert.Contains(t, body, `type="hidden"`,
		"csrf_token input must be hidden (not user-visible)")
}

// TestIngestForm_RendersCsrfTokenField pins the same
// contract for the ingest form. POST /ingest is on the
// protected list, so it needs the same hidden field.
func TestIngestForm_RendersCsrfTokenField(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ingest", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Contains(t, w.Body.String(), `name="csrf_token"`,
		"ingest form must render <input name=\"csrf_token\">")
}

// TestSearchForm_RendersCsrfTokenField pins the same
// contract for the search form.
func TestSearchForm_RendersCsrfTokenField(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Contains(t, w.Body.String(), `name="csrf_token"`,
		"search form must render <input name=\"csrf_token\">")
}

// TestCsrfMiddleware_RenderedTokenMatchesCookie pins
// the matching invariant: the csrf_token rendered in the
// form MUST match the cookie value set in the same
// response. Otherwise the form can never validate.
func TestCsrfMiddleware_RenderedTokenMatchesCookie(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/chat", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	// Extract cookie value.
	var cookieVal string
	for _, c := range w.Result().Cookies() {
		if c.Name == csrfCookieName {
			cookieVal = c.Value
			break
		}
	}
	require.NotEmpty(t, cookieVal, "cookie must be set on form render")

	// Extract rendered csrf_token field value from the HTML.
	// Look for the first hidden input named csrf_token and
	// read its value attribute.
	body := w.Body.String()
	const marker = `name="csrf_token"`
	idx := strings.Index(body, marker)
	require.GreaterOrEqual(t, idx, 0, "csrf_token input must be present")
	// Find the value= attribute within the same <input>.
	tagStart := strings.LastIndex(body[:idx], "<input")
	require.GreaterOrEqual(t, tagStart, 0, "input tag must start before csrf_token name")
	tagEnd := strings.Index(body[tagStart:], ">")
	require.GreaterOrEqual(t, tagEnd, 0, "input tag must close")
	tag := body[tagStart : tagStart+tagEnd]
	const valueMarker = `value="`
	vIdx := strings.Index(tag, valueMarker)
	require.GreaterOrEqual(t, vIdx, 0, "input must have value attribute")
	vEnd := strings.Index(tag[vIdx+len(valueMarker):], `"`)
	require.GreaterOrEqual(t, vEnd, 0, "value attribute must terminate")
	renderedVal := tag[vIdx+len(valueMarker) : vIdx+len(valueMarker)+vEnd]

	assert.Equal(t, cookieVal, renderedVal,
		"rendered csrf_token must match the cookie value")
}
