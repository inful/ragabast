package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/ragabast/internal/config"
)

// fakeGitHubServer is a minimal github.com OAuth + /user + /user/emails
// stand-in. It accepts the redirect URI we hand it, then issues a
// deterministic user identity that the test can read back through
// the userInfo struct.
//
//nolint:unparam // wantRedirect is parameterized so future tests can drive multiple redirect targets
func fakeGitHubServer(t *testing.T, wantRedirect string, user fakeUser) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// Authorization endpoint — accept any request with the
	// configured redirect_uri and respond by redirecting
	// back with a deterministic code. State is preserved
	// verbatim so the callback handler's CSRF check has
	// something to match.
	mux.HandleFunc("/login/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("redirect_uri"); got != wantRedirect {
			http.Error(w, "redirect_uri mismatch: "+got, http.StatusBadRequest)

			return
		}
		state := q.Get("state")
		// GitHub does not enforce PKCE; we still send the
		// challenge because our client does, and we don't
		// care that GitHub ignores it.
		http.Redirect(w, r, wantRedirect+"?code=fake-code&state="+url.QueryEscape(state), http.StatusFound)
	})

	// Token endpoint — accept any grant and return a
	// token. We don't introspect the body because the test
	// only checks that the round-trip succeeds.
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "bearer",
			"scope":        "read:user,user:email",
		})
	})

	// /user — return the configured identity.
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-access-token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    user.ID,
			"login": user.Username,
			"name":  user.Name,
			"email": user.Email,
		})
	})

	// /user/emails — GitHub's separate endpoint for
	// verified email addresses. Return the same email
	// the test configured.
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"email":    user.Email,
				"primary":  true,
				"verified": true,
			},
		})
	})

	return httptest.NewServer(mux)
}

// fakeUser is the identity a fake IdP will hand back.
type fakeUser struct {
	ID       int64  `json:"id"`
	Username string `json:"login"`
	Name     string `json:"name"`
	Email    string `json:"email"`
}

// buildOAuthHandlers wires an oauthHandlers struct against a
// fake IdP base URL. Used by the handler-level tests; the
// per-provider tests construct their own with the right
// config.
//
//nolint:unparam // baseURL is parameterized so multi-instance tests can vary the server origin
func buildOAuthHandlers(t *testing.T, provider config.OAuthProvider, baseURL string) *oauthHandlers {
	t.Helper()

	cfg := &config.Config{Auth: config.AuthConfig{
		SessionTTL: time.Hour,
		CookieName: "ragabast_session",
		Providers:  []config.OAuthProvider{provider},
	}}

	h, err := newOAuthHandlers(cfg, baseURL, newSessionStore(cfg.Auth.SessionTTL))
	require.NoError(t, err)

	return h
}

func TestNewOAuthHandlers_GitHubNoDiscovery(t *testing.T) {
	prov := config.OAuthProvider{
		Name: "gh", Type: "github",
		ClientID: "id", ClientSecret: "sec",
	}

	h := buildOAuthHandlers(t, prov, "https://app.example.com")

	require.Len(t, h.providers, 1)
	entry := h.providers["gh"]
	require.NotNil(t, entry)
	assert.Nil(t, entry.oidcProvider, "github preset must not require OIDC discovery")
	require.NotNil(t, entry.oauth2Config)
	assert.Equal(t, "https://app.example.com/auth/gh/callback", entry.oauth2Config.RedirectURL)
}

func TestOAuthLogin_RedirectsToIdP(t *testing.T) {
	user := fakeUser{ID: 42, Username: "alice", Name: "Alice", Email: "alice@example.com"}

	srv := fakeGitHubServer(t, "https://app.example.com/auth/gh/callback", user)
	defer srv.Close()

	prov := config.OAuthProvider{
		Name: "gh", Type: "github",
		ClientID: "id", ClientSecret: "sec",
	}
	// Override the GitHub endpoint via BaseURL — wait,
	// github preset ignores BaseURL. We patch the
	// oauth2Config's Endpoint directly after construction.
	h := buildOAuthHandlers(t, prov, "https://app.example.com")
	h.providers["gh"].oauth2Config.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/login/oauth/authorize",
		TokenURL: srv.URL + "/login/oauth/access_token",
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/login?next=/chat", nil)
	w := httptest.NewRecorder()

	h.handleProviderLogin(w, req, "gh")

	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	assert.Equal(t, http.StatusFound, res.StatusCode)
	loc := res.Header.Get("Location")
	assert.Contains(t, loc, srv.URL+"/login/oauth/authorize")
	assert.Contains(t, loc, "client_id=id")
	assert.Contains(t, loc, "redirect_uri=https%3A%2F%2Fapp.example.com%2Fauth%2Fgh%2Fcallback")
	assert.Contains(t, loc, "response_type=code")
	assert.Contains(t, loc, "code_challenge=")
	assert.Contains(t, loc, "code_challenge_method=S256")
	// state must be present — the callback uses it to
	// look up the pending flow.
	assert.Contains(t, loc, "state=")
	// State cookie must be set so the callback can
	// confirm the state came from this browser.
	var found bool
	for _, c := range res.Cookies() {
		if c.Name == oauthStateCookieName {
			found = true
			assert.True(t, c.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
		}
	}
	assert.True(t, found, "expected oauth_state cookie to be set")
}

func TestOAuthCallback_CreatesSessionAndRedirects(t *testing.T) {
	user := fakeUser{ID: 42, Username: "alice", Name: "Alice", Email: "alice@example.com"}

	srv := fakeGitHubServer(t, "https://app.example.com/auth/gh/callback", user)
	defer srv.Close()

	prov := config.OAuthProvider{
		Name: "gh", Type: "github",
		ClientID: "id", ClientSecret: "sec",
	}
	h := buildOAuthHandlers(t, prov, "https://app.example.com")
	h.providers["gh"].oauth2Config.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/login/oauth/authorize",
		TokenURL: srv.URL + "/login/oauth/access_token",
	}
	// Replace the userinfo fetcher with one that hits the
	// fake server instead of api.github.com.
	h.providers["gh"].userInfoFn = githubUserInfoFromBase(srv.URL)

	// Drive the login flow first to mint a real state +
	// pending flow record (mirrors what a real browser
	// would do).
	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/login?next=/chat", nil)
	loginW := httptest.NewRecorder()
	h.handleProviderLogin(loginW, loginReq, "gh")

	// Extract the state from the redirect URL and the
	// state cookie. We have to mimic the browser: the
	// cookie is set with the same state value.
	require.Equal(t, http.StatusFound, loginW.Result().StatusCode)

	loc, err := loginW.Result().Location()
	require.NoError(t, err)
	state := loc.Query().Get("state")

	// Now simulate the IdP's redirect back to the
	// callback.
	callbackURL := "/auth/gh/callback?code=fake-code&state=" + url.QueryEscape(state)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, callbackURL, nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: state})
	w := httptest.NewRecorder()

	h.handleProviderCallback(w, req, "gh")

	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	require.Equal(t, http.StatusFound, res.StatusCode, "callback must redirect to ?next=/chat")
	loc, err = res.Location()
	require.NoError(t, err)
	assert.Equal(t, "/chat", loc.String())

	// Session cookie must be set.
	var sessVal string
	for _, c := range res.Cookies() {
		if c.Name == "ragabast_session" {
			sessVal = c.Value
		}
	}
	require.NotEmpty(t, sessVal, "session cookie must be set")

	// The session must be retrievable from the store.
	sess, ok := h.sessions.Get(sessVal)
	require.True(t, ok)
	assert.Equal(t, "42", sess.Subject)
	assert.Equal(t, "alice", sess.Username)
	assert.Equal(t, "alice@example.com", sess.Email)
	assert.Equal(t, "gh", sess.ProviderName)
}

func TestOAuthCallback_RejectsMismatchedStateCookie(t *testing.T) {
	user := fakeUser{ID: 1, Username: "u", Email: "u@e.com"}

	srv := fakeGitHubServer(t, "https://app.example.com/auth/gh/callback", user)
	defer srv.Close()

	prov := config.OAuthProvider{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"}
	h := buildOAuthHandlers(t, prov, "https://app.example.com")
	h.providers["gh"].oauth2Config.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/login/oauth/authorize",
		TokenURL: srv.URL + "/login/oauth/access_token",
	}
	h.providers["gh"].userInfoFn = githubUserInfoFromBase(srv.URL)

	// Generate a real state via the login handler.
	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/login", nil)
	loginW := httptest.NewRecorder()
	h.handleProviderLogin(loginW, loginReq, "gh")
	loc, _ := loginW.Result().Location()
	state := loc.Query().Get("state")

	// But the callback request carries a DIFFERENT state
	// cookie — simulating a CSRF attack or a stale tab.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/callback?code=fake&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: state + "-tampered"})
	w := httptest.NewRecorder()
	h.handleProviderCallback(w, req, "gh")
	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	assert.Equal(t, http.StatusForbidden, res.StatusCode)
}

func TestOAuthCallback_RejectsUnknownProvider(t *testing.T) {
	user := fakeUser{ID: 1, Username: "u", Email: "u@e.com"}
	srv := fakeGitHubServer(t, "https://app.example.com/auth/gh/callback", user)
	defer srv.Close()
	prov := config.OAuthProvider{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"}
	h := buildOAuthHandlers(t, prov, "https://app.example.com")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/unknown/callback?code=x&state=y", nil)
	w := httptest.NewRecorder()
	h.handleProviderCallback(w, req, "unknown")
	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestOAuthCallback_RejectsProviderNameMismatch(t *testing.T) {
	user := fakeUser{ID: 1, Username: "u", Email: "u@e.com"}
	srv := fakeGitHubServer(t, "https://app.example.com/auth/gh/callback", user)
	defer srv.Close()
	cfg := &config.Config{Auth: config.AuthConfig{
		SessionTTL: time.Hour,
		CookieName: "ragabast_session",
		Providers: []config.OAuthProvider{
			{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
			{Name: "gl", Type: "gitlab", ClientID: "id", ClientSecret: "sec"},
		},
	}}
	h, err := newOAuthHandlers(cfg, "https://app.example.com", newSessionStore(time.Hour))
	require.NoError(t, err)

	// Mint a pending flow for "gh" but call back as "gl".
	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/login", nil)
	loginW := httptest.NewRecorder()
	h.handleProviderLogin(loginW, loginReq, "gh")
	loc, _ := loginW.Result().Location()
	state := loc.Query().Get("state")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gl/callback?code=fake&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: state})
	w := httptest.NewRecorder()
	h.handleProviderCallback(w, req, "gl")
	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	assert.Equal(t, http.StatusForbidden, res.StatusCode)
}

func TestOAuthCallback_RejectsUserNotInAllowedList(t *testing.T) {
	user := fakeUser{ID: 1, Username: "alice", Email: "alice@example.com"}
	srv := fakeGitHubServer(t, "https://app.example.com/auth/gh/callback", user)
	defer srv.Close()
	prov := config.OAuthProvider{
		Name: "gh", Type: "github",
		ClientID: "id", ClientSecret: "sec",
		AllowedUsers: []string{"bob"}, // alice not in list
	}
	h := buildOAuthHandlers(t, prov, "https://app.example.com")
	h.providers["gh"].oauth2Config.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/login/oauth/authorize",
		TokenURL: srv.URL + "/login/oauth/access_token",
	}
	h.providers["gh"].userInfoFn = githubUserInfoFromBase(srv.URL)

	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/login", nil)
	loginW := httptest.NewRecorder()
	h.handleProviderLogin(loginW, loginReq, "gh")
	loc, _ := loginW.Result().Location()
	state := loc.Query().Get("state")

	callbackURL := "/auth/gh/callback?code=fake-code&state=" + url.QueryEscape(state)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, callbackURL, nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: state})
	w := httptest.NewRecorder()
	h.handleProviderCallback(w, req, "gh")
	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	assert.Equal(t, http.StatusForbidden, res.StatusCode)
	body, _ := io.ReadAll(res.Body)
	assert.Contains(t, strings.ToLower(string(body)), "not in")
}

func TestLogout_ClearsSessionAndRedirects(t *testing.T) {
	user := fakeUser{ID: 1, Username: "u", Email: "u@e.com"}
	srv := fakeGitHubServer(t, "https://app.example.com/auth/gh/callback", user)
	defer srv.Close()
	prov := config.OAuthProvider{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"}
	h := buildOAuthHandlers(t, prov, "https://app.example.com")
	h.providers["gh"].oauth2Config.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/login/oauth/authorize",
		TokenURL: srv.URL + "/login/oauth/access_token",
	}
	h.providers["gh"].userInfoFn = githubUserInfoFromBase(srv.URL)

	// Drive a full login to populate a session.
	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/login", nil)
	loginW := httptest.NewRecorder()
	h.handleProviderLogin(loginW, loginReq, "gh")
	loc, _ := loginW.Result().Location()
	state := loc.Query().Get("state")
	cbReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	cbReq.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: state})
	cbW := httptest.NewRecorder()
	h.handleProviderCallback(cbW, cbReq, "gh")
	require.Equal(t, http.StatusFound, cbW.Result().StatusCode)
	var sessionVal string
	for _, c := range cbW.Result().Cookies() {
		if c.Name == "ragabast_session" {
			sessionVal = c.Value
		}
	}
	require.NotEmpty(t, sessionVal)
	_, ok := h.sessions.Get(sessionVal)
	require.True(t, ok)

	// Now log out.
	logoutReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sessionVal})
	logoutW := httptest.NewRecorder()
	h.handleLogout(logoutW, logoutReq)
	res := logoutW.Result()
	defer func() { _ = res.Body.Close() }()

	require.Equal(t, http.StatusFound, res.StatusCode)
	// Session must be gone from the store.
	_, ok = h.sessions.Get(sessionVal)
	assert.False(t, ok)
	// Session cookie must be cleared.
	var cleared bool
	for _, c := range res.Cookies() {
		if c.Name == "ragabast_session" {
			assert.Equal(t, -1, c.MaxAge)
			cleared = true
		}
	}
	assert.True(t, cleared, "expected cleared session cookie")
}

func TestLoginPage_SingleProviderRedirects(t *testing.T) {
	user := fakeUser{}
	srv := fakeGitHubServer(t, "https://app.example.com/auth/gh/callback", user)
	defer srv.Close()
	prov := config.OAuthProvider{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"}
	h := buildOAuthHandlers(t, prov, "https://app.example.com")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/login", nil)
	w := httptest.NewRecorder()
	h.handleLogin(w, req)
	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	require.Equal(t, http.StatusFound, res.StatusCode)
	loc, _ := res.Location()
	assert.Equal(t, "/auth/gh/login", loc.String())
}

func TestLoginPage_NoProvidersReturns503(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{CookieName: "ragabast_session", SessionTTL: time.Hour}}
	h, err := newOAuthHandlers(cfg, "https://app.example.com", newSessionStore(time.Hour))
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/login", nil)
	w := httptest.NewRecorder()
	h.handleLogin(w, req)
	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	assert.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
}

func TestNewOAuthHandlers_UnknownTypeErrors(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{
		Providers: []config.OAuthProvider{
			{Name: "weird", Type: "myspace", ClientID: "id", ClientSecret: "sec"},
		},
	}}
	_, err := newOAuthHandlers(cfg, "https://app.example.com", newSessionStore(time.Hour))
	require.Error(t, err)
}

func TestNewOAuthHandlers_ForgejoRequiresBaseURL(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{
		Providers: []config.OAuthProvider{
			{Name: "fj", Type: "forgejo", ClientID: "id", ClientSecret: "sec"},
		},
	}}
	_, err := newOAuthHandlers(cfg, "https://app.example.com", newSessionStore(time.Hour))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

// githubUserInfoFromBase returns a userInfoFn that talks to
// the fake GitHub server at baseURL instead of api.github.com.
// The token's AccessToken field is the literal the fake server
// checks for.
func githubUserInfoFromBase(baseURL string) func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
	return func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/user", nil)
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		res, err := c.Do(req)
		if err != nil {
			return userInfo{}, err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return userInfo{}, &httpStatusError{status: res.StatusCode, url: baseURL + "/user"}
		}
		var u struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
			return userInfo{}, err
		}
		// GitHub's primary email often lives on /user/emails.
		// Use the email from /user when present; otherwise
		// fetch the primary.
		if u.Email == "" {
			if fetched := fetchGitHubPrimaryEmail(ctx, c, tok, baseURL); fetched != "" {
				u.Email = fetched
			}
		}

		return userInfo{
			Subject:  formatInt(u.ID),
			Username: u.Login,
			Email:    u.Email,
			Name:     u.Name,
		}, nil
	}
}

func formatInt(i int64) string {
	return strconv.FormatInt(i, 10)
}
