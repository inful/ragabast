package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestFallbackHeader_AlwaysOnNavWithoutAuth pins the fix for
// issue #85: a basic top nav (Chat | Search | Documents | Ingest)
// renders on every page regardless of OAuth configuration.
// Operators running ragabast locally without configuring auth can
// navigate between pages without typing URLs or clicking the
// per-page Back button. Auth-specific bits (user display name,
// Sign in / Sign out) only render when OAuth is also
// configured — that conditional is exercised by the SignInLink
// and SignOutButton tests below.
func TestFallbackHeader_AlwaysOnNavWithoutAuth(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil // force fallback renderer

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "<nav", "a basic nav must render even without OAuth so /search, /documents, /ingest are discoverable")
	assert.Contains(t, body, `href="/"`, "nav must link back to the chat landing page")
	assert.Contains(t, body, `href="/search"`, "nav must link to /search")
	assert.Contains(t, body, `href="/documents"`, "nav must link to /documents")
	assert.Contains(t, body, `href="/ingest"`, "nav must link to /ingest")
	assert.NotContains(t, body, "Sign in", "no sign-in link should render when OAuth is not configured")
	assert.NotContains(t, body, "Sign out", "no sign-out button should render when OAuth is not configured")
}

// TestFallbackHeader_NavLinksPresentOnEveryPage pins the
// discovery contract: the always-on nav appears on every
// fallback page (chat, ingest, documents) so the operator can
// move between sections without typing URLs.
func TestFallbackHeader_NavLinksPresentOnEveryPage(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	for _, path := range []string{"/", "/ingest", "/documents"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			for _, link := range []string{`href="/"`, `href="/search"`, `href="/documents"`, `href="/ingest"`} {
				assert.Contains(t, body, link, "%s must surface the always-on nav link %q", path, link)
			}
		})
	}
}

// TestFallbackHeader_SignInLinkWhenOAuthConfiguredAndNotSignedIn
// verifies the page renders a "Sign in" link (not a
// sign-out button) when OAuth providers are configured but
// the request has no session cookie. The link threads the
// current URL into ?next= so the chooser page can land the
// user back where they came from after auth.
func TestFallbackHeader_SignInLinkWhenOAuthConfiguredAndNotSignedIn(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Providers = []config.OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
	}
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ingest", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "<nav", "nav bar should render when OAuth is configured")
	assert.Contains(t, body, "Sign in", "sign-in link should appear when not signed in")
	assert.NotContains(t, body, "Sign out", "sign-out button must not appear when not signed in")
	assert.Contains(t, body, "/auth/login?next=%2Fingest", "next= must thread the current URL")
}

// TestFallbackHeader_SignOutButtonWhenSignedIn confirms the
// signed-in state shows the user's display name and a
// sign-out form with the CSRF token. The form POSTs to
// /auth/logout which destroys the session and clears the
// cookie (the route handler is exercised by the OAuth
// tests; this test only confirms the form is rendered).
func TestFallbackHeader_SignOutButtonWhenSignedIn(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Providers = []config.OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
	}
	cfg.Auth.SessionTTL = time.Hour
	cfg.Auth.CookieName = "ragabast_session"

	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	// Mint a session directly in the store so the
	// middleware stamps it on the request context.
	sess := s.sessions.New()
	sess.Subject = "42"
	sess.Username = "alice"
	sess.ProviderName = "gh"
	s.sessions.Put(sess)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	assert.Contains(t, body, "alice (gh)", "display name must render")
	assert.Contains(t, body, `action="/auth/logout"`, "sign-out form must POST to /auth/logout")
	assert.Contains(t, body, `name="csrf_token"`, "sign-out form must carry the CSRF token")
	assert.Contains(t, body, "Sign out", "sign-out button must render")
	assert.NotContains(t, body, ">Sign in<", "sign-in link must NOT render when signed in")
}

// TestFallbackHeader_SignOutPostsSuccessfully is the
// end-to-end check: with a signed-in session, submitting
// the rendered sign-out form clears the cookie, destroys
// the session, and lands on /auth/login. This is the only
// test that doesn't rely on parsing HTML — it submits the
// form and asserts the side-effects.
func TestFallbackHeader_SignOutPostsSuccessfully(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Providers = []config.OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
	}
	cfg.Auth.SessionTTL = time.Hour

	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	sess := s.sessions.New()
	sess.Subject = "42"
	sess.Username = "alice"
	sess.ProviderName = "gh"
	s.sessions.Put(sess)

	// First GET / so the CSRF cookie is set on the
	// response. (GET /chat is a 302 to /, so we hit /
	// directly to keep the assertions tight.)
	getReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	getReq.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	getW := httptest.NewRecorder()
	s.router.ServeHTTP(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)
	getBody := getW.Body.String()
	csrfCookie := ""
	for _, c := range getW.Result().Cookies() {
		if strings.HasPrefix(c.Name, "ragabast_csrf") {
			csrfCookie = c.Value
		}
	}
	require.NotEmpty(t, csrfCookie, "csrf cookie must be set on the GET response")
	require.Contains(t, getBody, `name="csrf_token" value="`+csrfCookie+`"`, "chat form must embed the csrf token")

	// POST /auth/logout with the same cookie + csrf.
	logoutReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/logout",
		strings.NewReader("csrf_token="+csrfCookie))
	logoutReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logoutReq.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	logoutReq.AddCookie(&http.Cookie{Name: "ragabast_csrf", Value: csrfCookie})
	logoutW := httptest.NewRecorder()
	s.router.ServeHTTP(logoutW, logoutReq)

	require.Equal(t, http.StatusFound, logoutW.Code, "logout must redirect")
	loc, err := logoutW.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "/auth/login", loc.String())

	// Session must be gone from the store.
	_, ok := s.sessions.Get(sess.ID)
	assert.False(t, ok, "session must be deleted by sign-out")
}

// TestFallbackHeader_EscapesUserInfoInNavbar pins the
// security requirement that the navbar's user-controllable
// fields (display name from the IdP) flow through
// html/template's auto-escaping, the same as every other
// {{ }} substitution. An IdP that returns a username with
// embedded HTML can't break out of the navbar.
func TestFallbackHeader_EscapesUserInfoInNavbar(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Providers = []config.OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
	}
	cfg.Auth.SessionTTL = time.Hour

	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	// Subject must be set for the navbar to recognize
	// the user as signed in (UserFromContext returns a
	// User whose Subject is the OAuth sub claim, never
	// empty in production).
	sess := s.sessions.New()
	sess.Subject = "42"
	sess.Username = "<img src=x onerror=alert(1)>"
	sess.ProviderName = "gh"
	s.sessions.Put(sess)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	assert.NotContains(t, body, "<img src=x onerror=alert(1)>",
		"un-escaped username in navbar — XSS in fallback renderer")
	assert.Contains(t, body, "&lt;img src=x onerror=alert(1)&gt;",
		"username should appear in escaped form")
}
