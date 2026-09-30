package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

	mux.HandleFunc("/login/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("redirect_uri"); got != wantRedirect {
			http.Error(w, "redirect_uri mismatch: "+got, http.StatusBadRequest)

			return
		}
		state := q.Get("state")
		http.Redirect(w, r, wantRedirect+"?code=fake-code&state="+url.QueryEscape(state), http.StatusFound)
	})

	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "bearer",
			"scope":        "read:user,user:email",
		})
	})

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

// fakeOIDCServer is the minimum OIDC stand-in for tests:
// /.well-known/openid-configuration (discovery), /token
// (returns an access token + an unsigned ID token), and
// /userinfo. The discovery doc points at itself for all
// endpoints so go-oidc's NewProvider call can resolve
// them. The ID token uses alg=none so the test verifier
// can opt out of signature checks (real operators get
// RSA-signed tokens; this test only exercises the
// claim-extraction path).
func fakeOIDCServer(t *testing.T, clientID string, user fakeUser) *httptest.Server {
	t.Helper()

	issuer := ""

	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/authorize",
			"token_endpoint":                        issuer + "/token",
			"userinfo_endpoint":                     issuer + "/userinfo",
			"jwks_uri":                              issuer + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256", "none"},
		})
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
			`{"sub":"%d","preferred_username":"%s","name":"%s","email":"%s","aud":"%s","iss":"%s","exp":9999999999}`,
			user.ID, user.Username, user.Name, user.Email, clientID, issuer,
		)))
		idToken := header + "." + payload + "."

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access-token",
			"id_token":     idToken,
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})

	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-access-token" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub":   strconv.FormatInt(user.ID, 10),
			"name":  user.Name,
			"email": user.Email,
			"login": user.Username,
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})

	srv := httptest.NewServer(mux)
	issuer = srv.URL

	return srv
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
	require.NotNil(t, entry.oauth2Config)
	assert.Equal(t, "https://app.example.com/auth/gh/callback", entry.oauth2Config.RedirectURL)
	// github.com is hardcoded; Endpoint must point at
	// api.github.com rather than the operator-configured
	// base URL.
	assert.Equal(t, "https://github.com/login/oauth/authorize", entry.oauth2Config.Endpoint.AuthURL)
}

func TestNewOAuthHandlers_OIDCRunsDiscovery(t *testing.T) {
	user := fakeUser{ID: 7, Username: "carol", Name: "Carol", Email: "carol@example.com"}
	srv := fakeOIDCServer(t, "my-client", user)
	defer srv.Close()

	prov := config.OAuthProvider{
		Name: "corp", Type: "oidc",
		ClientID:     "my-client",
		ClientSecret: "my-secret",
		DiscoveryURL: srv.URL,
	}

	h := buildOAuthHandlers(t, prov, "https://app.example.com")

	entry := h.providers["corp"]
	require.NotNil(t, entry)
	require.NotNil(t, entry.oauth2Config)
	assert.Equal(t, "https://app.example.com/auth/corp/callback", entry.oauth2Config.RedirectURL)
	assert.Equal(t, srv.URL+"/token", entry.oauth2Config.Endpoint.TokenURL)
}

func TestNewOAuthHandlers_OIDCBadDiscoveryURL(t *testing.T) {
	prov := config.OAuthProvider{
		Name: "broken", Type: "oidc",
		ClientID: "id", ClientSecret: "sec",
		DiscoveryURL: "http://127.0.0.1:1/no-such-server",
	}

	_, err := newOAuthHandlers(
		&config.Config{Auth: config.AuthConfig{
			Providers:  []config.OAuthProvider{prov},
			CookieName: "ragabast_session",
			SessionTTL: time.Hour,
		}},
		"https://app.example.com",
		newSessionStore(time.Hour),
	)
	require.Error(t, err)
}

func TestOAuthLogin_RedirectsToIdP(t *testing.T) {
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
	assert.Contains(t, loc, "state=")

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
	h.providers["gh"].userInfoFn = githubUserInfo(srv.URL)

	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/login?next=/chat", nil)
	loginW := httptest.NewRecorder()
	h.handleProviderLogin(loginW, loginReq, "gh")

	require.Equal(t, http.StatusFound, loginW.Result().StatusCode)

	loc, err := loginW.Result().Location()
	require.NoError(t, err)
	state := loc.Query().Get("state")

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

	var sessVal string
	for _, c := range res.Cookies() {
		if c.Name == "ragabast_session" {
			sessVal = c.Value
		}
	}
	require.NotEmpty(t, sessVal, "session cookie must be set")

	sess, ok := h.sessions.Get(sessVal)
	require.True(t, ok)
	assert.Equal(t, "42", sess.Subject)
	assert.Equal(t, "alice", sess.Username)
	assert.Equal(t, "alice@example.com", sess.Email)
	assert.Equal(t, "gh", sess.ProviderName)
}

func TestOAuthCallback_OIDCEndToEnd(t *testing.T) {
	user := fakeUser{ID: 99, Username: "carol", Name: "Carol", Email: "carol@example.com"}
	srv := fakeOIDCServer(t, "my-client", user)
	defer srv.Close()

	prov := config.OAuthProvider{
		Name: "corp", Type: "oidc",
		ClientID: "my-client", ClientSecret: "my-secret",
		DiscoveryURL: srv.URL,
	}
	h := buildOAuthHandlers(t, prov, "https://app.example.com")

	// The fake IdP issues an unsigned (alg=none) ID
	// token — a real IdP would sign with RS256. go-oidc
	// rejects alg=none outright for security, so we
	// swap userInfoFn for a test stub that decodes the
	// unsigned JWT manually. The end-to-end path
	// (discovery → token exchange → callback → mint
	// session) is still exercised; only the signature
	// verification step is bypassed.
	h.providers["corp"].userInfoFn = oidcUserInfoFromRawTokenForTest()

	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/corp/login?next=/chat", nil)
	loginW := httptest.NewRecorder()
	h.handleProviderLogin(loginW, loginReq, "corp")

	require.Equal(t, http.StatusFound, loginW.Result().StatusCode)

	loc, err := loginW.Result().Location()
	require.NoError(t, err)
	state := loc.Query().Get("state")
	code := "fake-code"

	callbackURL := "/auth/corp/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, callbackURL, nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookieName, Value: state})
	w := httptest.NewRecorder()
	h.handleProviderCallback(w, req, "corp")

	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, http.StatusFound, res.StatusCode, "OIDC callback must redirect; body=%s", readBody(t, res))

	var sessVal string
	for _, c := range res.Cookies() {
		if c.Name == "ragabast_session" {
			sessVal = c.Value
		}
	}
	require.NotEmpty(t, sessVal, "session cookie must be set")

	sess, ok := h.sessions.Get(sessVal)
	require.True(t, ok)
	assert.Equal(t, "99", sess.Subject, "sub claim should be the user id")
	assert.Equal(t, "carol", sess.Username, "preferred_username should be the username")
	assert.Equal(t, "carol@example.com", sess.Email)
	assert.Equal(t, "corp", sess.ProviderName)
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
	h.providers["gh"].userInfoFn = githubUserInfo(srv.URL)

	loginReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/gh/login", nil)
	loginW := httptest.NewRecorder()
	h.handleProviderLogin(loginW, loginReq, "gh")
	loc, _ := loginW.Result().Location()
	state := loc.Query().Get("state")

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
		AllowedUsers: []string{"bob"},
	}
	h := buildOAuthHandlers(t, prov, "https://app.example.com")
	h.providers["gh"].oauth2Config.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/login/oauth/authorize",
		TokenURL: srv.URL + "/login/oauth/access_token",
	}
	h.providers["gh"].userInfoFn = githubUserInfo(srv.URL)

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
	h.providers["gh"].userInfoFn = githubUserInfo(srv.URL)

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

	logoutReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sessionVal})
	logoutW := httptest.NewRecorder()
	h.handleLogout(logoutW, logoutReq)
	res := logoutW.Result()
	defer func() { _ = res.Body.Close() }()

	require.Equal(t, http.StatusFound, res.StatusCode)
	_, ok = h.sessions.Get(sessionVal)
	assert.False(t, ok)
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

func TestLoginPage_MultipleProvidersRendersChooser(t *testing.T) {
	user := fakeUser{}
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

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/login?next=/chat", nil)
	w := httptest.NewRecorder()
	h.handleLogin(w, req)
	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
	body, _ := io.ReadAll(res.Body)
	html := string(body)
	assert.Contains(t, html, "Sign in with GitHub")
	assert.Contains(t, html, "Sign in with GitLab")
	assert.Contains(t, html, "/auth/gh/login")
	assert.Contains(t, html, "/auth/gl/login")
	assert.Contains(t, html, "next=%2fchat", "next query parameter must be threaded into the chooser links")
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

// readBody is a tiny helper for tests that want to surface
// the response body in failure messages.
func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	body, _ := io.ReadAll(res.Body)

	return string(body)
}

// oidcUserInfoFromRawTokenForTest is the test-only
// replacement for oidcUserInfo when the fake IdP issues
// an unsigned JWT. It decodes the id_token claims
// directly (skipping signature verification, which
// go-oidc forbids for alg=none in real builds) and
// returns the same userInfo shape so the rest of the
// callback path is exercised unchanged.
func oidcUserInfoFromRawTokenForTest() func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
	return func(_ context.Context, _ *http.Client, tok *oauth2.Token) (userInfo, error) {
		rawIDToken, ok := tok.Extra("id_token").(string)
		if !ok || rawIDToken == "" {
			return userInfo{}, errors.New("OIDC token response missing id_token")
		}
		parts := strings.Split(rawIDToken, ".")
		if len(parts) < 2 {
			return userInfo{}, errors.New("malformed id_token")
		}
		// RawURLEncoding (no padding) is what JWS uses.
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return userInfo{}, fmt.Errorf("id_token payload decode: %w", err)
		}
		var claims struct {
			Sub               string `json:"sub"`
			PreferredUsername string `json:"preferred_username"`
			Email             string `json:"email"`
			Name              string `json:"name"`
		}
		if err := json.Unmarshal(payload, &claims); err != nil {
			return userInfo{}, fmt.Errorf("id_token claims decode: %w", err)
		}

		return userInfo{
			Subject:  claims.Sub,
			Username: claims.PreferredUsername,
			Email:    claims.Email,
			Name:     claims.Name,
		}, nil
	}
}

// TestNewOAuthHandlers_DefaultProviderFiltersToOne pins the
// v0.10.7 behavior: when DefaultProviderName is set, the
// providers map is restricted to that one entry. The other
// configured providers are validated at startup (so a typo
// in client_id still surfaces) but never registered as routes
// — meaning /auth/<other>/login returns 404 and the chooser
// never renders.
func TestNewOAuthHandlers_DefaultProviderFiltersToOne(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{
		SessionTTL:          time.Hour,
		CookieName:          "ragabast_session",
		DefaultProviderName: "work-gitlab",
		Providers: []config.OAuthProvider{
			{Name: "github", Type: "github", ClientID: "id", ClientSecret: "sec"},
			{Name: "work-gitlab", Type: "gitlab", ClientID: "id2", ClientSecret: "sec2", BaseURL: "https://gitlab.work"},
			{Name: "keycloak", Type: "oidc", ClientID: "id3", ClientSecret: "sec3", DiscoveryURL: "https://kc.example.com"},
		},
	}}

	h, err := newOAuthHandlers(cfg, "https://app.example.com", newSessionStore(cfg.Auth.SessionTTL))
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Len(t, h.providers, 1,
		"providers map must contain only the default")
	_, ok := h.providers["work-gitlab"]
	assert.True(t, ok, "default provider must be registered")
	_, ok = h.providers["github"]
	assert.False(t, ok, "non-default providers must NOT be registered")
	_, ok = h.providers["keycloak"]
	assert.False(t, ok, "non-default providers must NOT be registered")
}

// TestNewOAuthHandlers_DefaultProviderNotConfigured pins the
// failure mode: Validate should catch this at config-load
// time, but newOAuthHandlers defends in depth — if a test
// or a future code path constructs a config without calling
// Validate first, the function still errors out rather than
// silently producing an empty providers map.
func TestNewOAuthHandlers_DefaultProviderNotConfigured(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{
		SessionTTL:          time.Hour,
		CookieName:          "ragabast_session",
		DefaultProviderName: "nonexistent",
		Providers: []config.OAuthProvider{
			{Name: "github", Type: "github", ClientID: "id", ClientSecret: "sec"},
		},
	}}

	_, err := newOAuthHandlers(cfg, "https://app.example.com", newSessionStore(cfg.Auth.SessionTTL))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent",
		"error must name the bad default so the operator can see what they typo'd")
}

// TestOAuthLogin_DefaultProviderSkipsChooser pins the
// end-to-end behavior: with DefaultProviderName set, the
// /auth/login endpoint auto-redirects to the default
// provider (single-provider path) instead of rendering
// the chooser. This is the behavior operators expect when
// they pin to one IdP — the chooser never even renders.
func TestOAuthLogin_DefaultProviderSkipsChooser(t *testing.T) {
	user := fakeUser{ID: 42, Username: "alice", Name: "Alice", Email: "alice@example.com"}

	srv := fakeGitHubServer(t, "https://app.example.com/auth/work-gitlab/callback", user)
	defer srv.Close()

	cfg := &config.Config{Auth: config.AuthConfig{
		SessionTTL:          time.Hour,
		CookieName:          "ragabast_session",
		DefaultProviderName: "work-gitlab",
		Providers: []config.OAuthProvider{
			{Name: "github", Type: "github", ClientID: "id", ClientSecret: "sec"},
			{Name: "work-gitlab", Type: "gitlab", ClientID: "id2", ClientSecret: "sec2", BaseURL: srv.URL},
		},
	}}

	// Override the work-gitlab entry's Endpoint to point at
	// the fake server so the test doesn't need a real
	// gitlab at srv.URL.
	h, err := newOAuthHandlers(cfg, "https://app.example.com", newSessionStore(cfg.Auth.SessionTTL))
	require.NoError(t, err)
	h.providers["work-gitlab"].oauth2Config.Endpoint = oauth2.Endpoint{
		AuthURL:  srv.URL + "/login/oauth/authorize",
		TokenURL: srv.URL + "/login/oauth/access_token",
	}
	h.providers["work-gitlab"].userInfoFn = gitlabUserInfo(srv.URL)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/login", nil)
	w := httptest.NewRecorder()
	h.handleLogin(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	require.Equal(t, http.StatusFound, res.StatusCode,
		"with default set, /auth/login must auto-redirect (302) not render chooser (200)")

	loc, err := res.Location()
	require.NoError(t, err)
	assert.Equal(t, "/auth/work-gitlab/login", loc.Path,
		"redirect target must be the default provider, not the chooser or any non-default provider")
}
