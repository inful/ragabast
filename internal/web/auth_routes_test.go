package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestNewServer_NoProviders_NoOAuthRoutes mounts the server
// with auth.providers empty and asserts /auth/login returns
// 503. The /auth/me endpoint should report
// authenticated=false. The historical single-user /
// bearer-token install path stays open.
func TestNewServer_NoProviders_NoOAuthRoutes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "test-token"

	srv := NewServer(cfg, &fakeHumaService{})

	t.Run("/auth/login returns 503", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/login", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("/auth/me reports anonymous", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/me", nil)
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body, _ := io.ReadAll(w.Body)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, false, got["authenticated"])
	})

	t.Run("bearer-token path still works", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// TestNewServer_WithProviders_LoginPageRedirects mounts
// the server with one OAuth provider configured. The
// single-provider shortcut should redirect /auth/login to
// /auth/<name>/login without an intermediate HTML page.
func TestNewServer_WithProviders_LoginPageRedirects(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Providers = []config.OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
	}

	srv := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/login", nil)
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	loc, err := w.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "/auth/gh/login", loc.String())
}

// TestNewServer_SessionCookie_AllowsMeEndpoint mints a
// session directly in the store, then hits /auth/me via
// the cookie and asserts the JSON payload carries the
// expected fields.
func TestNewServer_SessionCookie_AllowsMeEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Providers = []config.OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
	}
	cfg.Auth.SessionTTL = time.Hour
	cfg.Auth.CookieName = "ragabast_session"

	srv := NewServer(cfg, &fakeHumaService{})

	sess := srv.sessions.New()
	sess.Subject = "42"
	sess.Username = "alice"
	sess.Email = "alice@example.com"
	sess.ProviderName = "gh"
	srv.sessions.Put(sess)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	body, _ := io.ReadAll(w.Body)
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, true, got["authenticated"])
	assert.Equal(t, "42", got["subject"])
	assert.Equal(t, "alice", got["username"])
	assert.Equal(t, "alice@example.com", got["email"])
	assert.Equal(t, "gh", got["provider"])
	assert.Equal(t, "user", got["role"])
}

// TestAuthMiddleware_PublicRoute_AllowsAuthRoute confirms
// isPublicRoute covers the /auth/* tree so the IdP
// redirect lands on /auth/<provider>/callback even when
// the user has no session.
func TestAuthMiddleware_PublicRoute_AllowsAuthRoute(t *testing.T) {
	assert.True(t, isPublicRoute(http.MethodGet, "/auth/login"))
	assert.True(t, isPublicRoute(http.MethodGet, "/auth/gh/login"))
	assert.True(t, isPublicRoute(http.MethodGet, "/auth/gh/callback"))
	assert.True(t, isPublicRoute(http.MethodGet, "/auth/me"))
	assert.False(t, isPublicRoute(http.MethodPost, "/auth/gh/callback"), "POST /auth/callback must be rejected (CSRF)")
	assert.False(t, isPublicRoute(http.MethodPost, "/auth/login"), "POST /auth/login must require a session to be a no-op signal")
}
