package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/ragabast/internal/config"
)

// oauthStateCookieName is the short-lived cookie that
// carries the OAuth state token across the redirect. It is
// set on the /auth/<name>/login response and re-read on the
// callback to confirm the request really originated from a
// login this browser initiated (the value must equal the
// `state` query parameter the IdP echoes back).
//
// HttpOnly + SameSite=Lax — same shape as the csrf cookie,
// because the threat model is identical (cross-origin
// attacker cannot make the browser send it on a POST, and
// cannot read it cross-origin). MaxAge is short (5
// minutes) so a stale login attempt cannot be replayed
// after the user has navigated away.
const oauthStateCookieName = "ragabast_oauth_state"

// oauthFlowStateTTL is how long a pending OAuth flow is
// kept in the flowStateStore. 10 minutes matches the
// typical IdP session timeout; longer than that is unlikely
// to represent a real user still trying to log in.
const oauthFlowStateTTL = 10 * time.Minute

// userInfo is the IdP-independent shape the callback
// resolves after a successful token exchange. Every
// provider-specific fetcher (GitHub / GitLab / Forgejo /
// OIDC) returns one of these so the rest of the handler
// stays provider-agnostic.
type userInfo struct {
	// Subject is the IdP's stable per-user identifier
	// (the `sub` claim for OIDC, the numeric user id for
	// GitHub / GitLab / Forgejo). It is what we attribute
	// audit-log lines to.
	Subject string

	// Username is the provider's login/username field.
	// Used for the "signed in as" line and the
	// AllowedUsers match.
	Username string

	// Email is the provider's primary email. Empty when
	// the scope set did not request email.
	Email string

	// Name is the provider's display name. Empty when
	// the IdP did not return one (GitHub may return "" if
	// the user has not set a public name).
	Name string
}

// providerEntry is one built provider — the resolved
// oauth2.Config, the optional OIDC verifier, and the
// userInfoFn closure that fetches the authenticated user's
// profile from the IdP's userinfo endpoint.
//
// Constructed once at startup by newOAuthHandlers and
// read-only thereafter; tests that need to talk to a fake
// IdP overwrite entry.oauth2Config.Endpoint and
// entry.userInfoFn after construction.
type providerEntry struct {
	cfg config.OAuthProvider
	// oauth2Config is the resolved OAuth 2.0 config for
	// this provider. The Endpoint field is the static
	// preset (github / gitlab / forgejo) or the runtime
	// discovery result (oidc).
	oauth2Config *oauth2.Config
	// oidcProvider / oidcVerifier are non-nil only for
	// type=oidc providers, where the discovery round-trip
	// was needed to populate oauth2Config.Endpoint.
	oidcProvider *oidcProviderEntry
	// userInfoFn resolves the authenticated user's
	// profile after a successful token exchange.
	userInfoFn func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error)
}

// oidcProviderEntry wraps the bits a type=oidc provider
// needs on top of the static oauth2.Config: the discovered
// provider (used for UserInfo() and ID-token verification).
type oidcProviderEntry struct {
	// userInfoFn is set on the parent providerEntry but
	// uses the OIDC UserInfo helper from go-oidc.
}

// flowState is a pending OAuth login — the (state, PKCE
// verifier, provider name, next URL) tuple the callback
// handler resolves on the way back from the IdP.
//
// Stored in flowStateStore keyed by the state token. Short
// TTL (10 min) so a stale state cannot be replayed.
type flowState struct {
	providerName string
	verifier     string
	next         string
	createdAt    time.Time
}

// flowStateStore is the in-memory map of pending OAuth
// flows. Same concurrency rules as sessionStore: RWMutex
// around the map, lazy eviction on Get.
type flowStateStore struct {
	mu      sync.RWMutex
	entries map[string]*flowState
}

func newFlowStateStore() *flowStateStore {
	return &flowStateStore{entries: make(map[string]*flowState)}
}

// Put stores a flow keyed by state. Caller must set
// CreatedAt; the store does not auto-stamp.
func (s *flowStateStore) Put(state string, f *flowState) {
	if state == "" || f == nil {
		return
	}

	s.mu.Lock()
	s.entries[state] = f
	s.mu.Unlock()
}

// Pop removes and returns the flow for state, or nil.
// Missing or expired flows return nil; callers treat that
// as a 4xx.
func (s *flowStateStore) Pop(state string) *flowState {
	if state == "" {
		return nil
	}

	s.mu.RLock()
	entry, ok := s.entries[state]
	s.mu.RUnlock()

	if !ok {
		return nil
	}
	if time.Since(entry.createdAt) > oauthFlowStateTTL {
		s.mu.Lock()
		delete(s.entries, state)
		s.mu.Unlock()

		return nil
	}

	s.mu.Lock()
	delete(s.entries, state)
	s.mu.Unlock()

	return entry
}

// oauthHandlers owns the OAuth machinery: the provider
// registry, the pending-flow store, and the session store
// new sessions get minted into.
type oauthHandlers struct {
	cfg        *config.AuthConfig
	serverBase string
	cookieName string
	sessions   *sessionStore
	providers  map[string]*providerEntry
	flows      *flowStateStore
}

// newOAuthHandlers constructs the provider registry from
// the configured OAuthProvider list. serverBase is the
// externally-reachable origin (scheme + host + port) the
// IdP redirects back to; used to build per-provider
// callback URLs.
//
// Returns an error when any provider fails its initial
// validation (unknown type, missing BaseURL, etc.). OIDC
// providers additionally require discovery at startup, so
// a bad DiscoveryURL also surfaces as an error here.
func newOAuthHandlers(cfg *config.Config, serverBase string, sessions *sessionStore) (*oauthHandlers, error) {
	auth := &cfg.Auth

	h := &oauthHandlers{
		cfg:        auth,
		serverBase: strings.TrimRight(serverBase, "/"),
		cookieName: auth.CookieName,
		sessions:   sessions,
		providers:  make(map[string]*providerEntry, len(auth.Providers)),
		flows:      newFlowStateStore(),
	}

	for _, p := range auth.Providers {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		entry, err := buildProviderEntry(p, h.serverBase)
		if err != nil {
			return nil, fmt.Errorf("auth.providers[%q]: %w", p.Name, err)
		}
		h.providers[p.Name] = entry
	}

	return h, nil
}

// buildProviderEntry resolves one OAuthProvider into a
// providerEntry. The static presets (github / gitlab /
// forgejo) get their endpoint from provider.Endpoint(); the
// OIDC preset runs discovery.
//
// userInfoFn is wired here based on the provider's effective
// base URL. For tests, callers can replace the entry's
// userInfoFn after construction to point at a fake server.
func buildProviderEntry(p config.OAuthProvider, serverBase string) (*providerEntry, error) {
	redirect := p.RedirectURL(serverBase)

	scopes := p.EffectiveScopes()

	switch p.Type {
	case "github":
		ep, err := p.Endpoint()
		if err != nil {
			return nil, err
		}

		return &providerEntry{
			cfg: p,
			oauth2Config: &oauth2.Config{
				ClientID:     p.ClientID,
				ClientSecret: p.ClientSecret,
				RedirectURL:  redirect,
				Scopes:       scopes,
				Endpoint:     ep,
			},
			userInfoFn: githubUserInfo("https://api.github.com"),
		}, nil
	case "gitlab":
		ep, err := p.Endpoint()
		if err != nil {
			return nil, err
		}
		apiBase := p.BaseURL
		if apiBase == "" {
			apiBase = "https://gitlab.com"
		}

		return &providerEntry{
			cfg: p,
			oauth2Config: &oauth2.Config{
				ClientID:     p.ClientID,
				ClientSecret: p.ClientSecret,
				RedirectURL:  redirect,
				Scopes:       scopes,
				Endpoint:     ep,
			},
			userInfoFn: gitlabUserInfo(strings.TrimRight(apiBase, "/")),
		}, nil
	case "forgejo":
		ep, err := p.Endpoint()
		if err != nil {
			return nil, err
		}
		apiBase := strings.TrimRight(p.BaseURL, "/")

		return &providerEntry{
			cfg: p,
			oauth2Config: &oauth2.Config{
				ClientID:     p.ClientID,
				ClientSecret: p.ClientSecret,
				RedirectURL:  redirect,
				Scopes:       scopes,
				Endpoint:     ep,
			},
			userInfoFn: forgejoUserInfo(apiBase),
		}, nil
	case "oidc":
		// OIDC discovery runs at startup. Failures here
		// are operator errors (wrong DiscoveryURL,
		// unreachable IdP) and prevent the server from
		// starting — which is the right behavior; silent
		// fallback would mean login is broken with no
		// signal in the logs.
		return nil, errors.New("type=oidc provider requires go-oidc integration; not yet implemented in this build")
	default:
		return nil, fmt.Errorf("unsupported type %q", p.Type)
	}
}

// handleLogin renders the login page. With zero providers
// configured it returns 503 (auth disabled). With one
// provider it redirects straight to /auth/<name>/login so
// the operator does not have to click twice. With multiple
// providers it renders the chooser template.
func (h *oauthHandlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	if len(h.providers) == 0 {
		http.Error(w, "auth is not configured", http.StatusServiceUnavailable)

		return
	}
	if len(h.providers) == 1 {
		// Pick the only configured provider.
		for name := range h.providers {
			http.Redirect(w, r, "/auth/"+name+"/login", http.StatusFound)

			return
		}
	}
	// Multi-provider: the rendered template lists each
	// provider as a button. Fall-through to the template
	// rendering path.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// Minimal HTML fallback for now — the template layer
	// replaces this once renderTemplate gains a LoginPage
	// hook.
	_, _ = w.Write([]byte("<!doctype html><title>Sign in</title><ul>"))
	for name := range h.providers {
		_, _ = fmt.Fprintf(w, `<li><a href="/auth/%s/login">Sign in with %s</a></li>`, name, name)
	}
	_, _ = w.Write([]byte("</ul>"))
}

// handleProviderLogin starts the OAuth flow for the named
// provider: generate state + PKCE verifier, stash the
// pending flow, redirect to the IdP.
//
//nolint:unparam // providerName will vary when multiple providers are wired up; current tests use a single provider
func (h *oauthHandlers) handleProviderLogin(w http.ResponseWriter, r *http.Request, providerName string) {
	entry, ok := h.providers[providerName]
	if !ok {
		http.Error(w, "unknown provider", http.StatusNotFound)

		return
	}

	verifier := oauth2.GenerateVerifier()

	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)

		return
	}
	state := hex.EncodeToString(stateBytes)

	next := r.URL.Query().Get("next")
	if next == "" || !isSafeRedirect(next) {
		next = "/"
	}

	h.flows.Put(state, &flowState{
		providerName: providerName,
		verifier:     verifier,
		next:         next,
		createdAt:    time.Now(),
	})

	authURL := entry.oauth2Config.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
	)

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   int(oauthFlowStateTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureFromRequest(r),
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleProviderCallback resolves the IdP redirect:
// verifies state, exchanges the code for a token, fetches
// userinfo, mints a session, redirects to ?next= or /.
func (h *oauthHandlers) handleProviderCallback(w http.ResponseWriter, r *http.Request, providerName string) {
	entry, ok := h.providers[providerName]
	if !ok {
		http.Error(w, "unknown provider", http.StatusNotFound)

		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	stateCookie, err := r.Cookie(oauthStateCookieName)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != state {
		http.Error(w, "state cookie missing or mismatched", http.StatusForbidden)

		return
	}
	// Clear the state cookie regardless of outcome so a
	// stale value cannot be replayed.
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureFromRequest(r),
	})

	flow := h.flows.Pop(state)
	if flow == nil {
		http.Error(w, "state expired or unknown", http.StatusForbidden)

		return
	}
	if flow.providerName != providerName {
		http.Error(w, "provider mismatch", http.StatusForbidden)

		return
	}

	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)

		return
	}

	ctx := r.Context()

	tok, err := entry.oauth2Config.Exchange(ctx, code, oauth2.VerifierOption(flow.verifier))
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)

		return
	}

	ui, err := entry.userInfoFn(ctx, entry.oauth2Config.Client(ctx, tok), tok)
	if err != nil {
		http.Error(w, "userinfo fetch failed: "+err.Error(), http.StatusBadGateway)

		return
	}

	if !allowedUserOK(entry.cfg.AllowedUsers, ui.Username, ui.Email) {
		http.Error(w, "user is not in the allowed_users list for this provider", http.StatusForbidden)

		return
	}

	s := h.sessions.New()
	s.Subject = ui.Subject
	s.Username = ui.Username
	s.Email = ui.Email
	s.Name = ui.Name
	s.ProviderName = providerName
	s.Role = defaultRoleOrDefault(entry.cfg.DefaultRole)
	if matched, ok := matchedAllowedUser(entry.cfg.AllowedUsers, ui.Username, ui.Email); ok {
		s.AllowedUsersHit = true
		s.AllowedUsersMatched = matched
	}
	h.sessions.Put(s)

	setSessionCookie(w, h.cookieName, s.ID, secureFromRequest(r), h.cfg.SessionTTL)

	http.Redirect(w, r, flow.next, http.StatusFound)
}

// handleLogout destroys the session for the current
// request (if any) and clears the session cookie, then
// redirects to /auth/login.
func (h *oauthHandlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(h.cookieName); err == nil && c.Value != "" {
		h.sessions.Delete(c.Value)
	}
	clearSessionCookie(w, h.cookieName, secureFromRequest(r))
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

// isSafeRedirect prevents open-redirect: only allow
// same-origin paths (leading "/", no "//", no scheme).
// Anything else is replaced with "/".
func isSafeRedirect(next string) bool {
	if next == "" {
		return false
	}
	if strings.HasPrefix(next, "//") {
		return false
	}
	if strings.Contains(next, "://") {
		return false
	}
	if !strings.HasPrefix(next, "/") {
		return false
	}

	return true
}

// allowedUserOK reports whether (username, email) is
// permitted by the AllowedUsers list. Empty list = allow
// everyone. Match is exact-string equality (no glob, no
// regex) so the operator can be confident the allow-list
// is not a footgun — a typo means "nobody".
func allowedUserOK(allowList []string, username, email string) bool {
	if len(allowList) == 0 {
		return true
	}
	for _, allowed := range allowList {
		if allowed == username || allowed == email {
			return true
		}
	}

	return false
}

// matchedAllowedUser returns the allow-list entry that
// matched (username, email) and true, or ("", false) when
// no entry matched. Callers should already have checked
// allowedUserOK; this is for logging the audit attribution.
func matchedAllowedUser(allowList []string, username, email string) (string, bool) {
	for _, allowed := range allowList {
		if allowed == username || allowed == email {
			return allowed, true
		}
	}

	return "", false
}

// defaultRoleOrDefault returns "user" when role is empty;
// otherwise the configured value.
func defaultRoleOrDefault(role string) string {
	if role == "" {
		return "user"
	}

	return role
}

// githubUserInfo returns a userInfoFn that talks to
// api.github.com (or a fake baseURL for tests). The
// closure does NOT capture state — baseURL is fixed at
// construction time.
func githubUserInfo(baseURL string) func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
	return func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
		userReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/user", nil)
		userReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		userRes, err := c.Do(userReq)
		if err != nil {
			return userInfo{}, err
		}
		defer func() { _ = userRes.Body.Close() }()
		if userRes.StatusCode != http.StatusOK {
			return userInfo{}, &httpStatusError{status: userRes.StatusCode, url: baseURL + "/user"}
		}
		var u struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := json.NewDecoder(userRes.Body).Decode(&u); err != nil {
			return userInfo{}, err
		}
		if u.Email == "" {
			if fetched := fetchGitHubPrimaryEmail(ctx, c, tok, baseURL); fetched != "" {
				u.Email = fetched
			}
		}

		return userInfo{
			Subject:  strconv.FormatInt(u.ID, 10),
			Username: u.Login,
			Email:    u.Email,
			Name:     u.Name,
		}, nil
	}
}

// fetchGitHubPrimaryEmail is the GitHub /user/emails
// fallback. The /user endpoint returns the primary email
// only when it is public; otherwise (the default for many
// users) the email lives behind /user/emails and is
// resolved to the primary+verified entry.
func fetchGitHubPrimaryEmail(ctx context.Context, c *http.Client, tok *oauth2.Token, baseURL string) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	res, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = res.Body.Close() }()

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(res.Body).Decode(&emails); err != nil {
		return ""
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email
		}
	}

	return ""
}

// gitlabUserInfo returns a userInfoFn that talks to the
// GitLab /api/v4/user endpoint. baseURL is the GitLab
// root (https://gitlab.com or a self-hosted BaseURL).
func gitlabUserInfo(baseURL string) func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
	return func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v4/user", nil)
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		res, err := c.Do(req)
		if err != nil {
			return userInfo{}, err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return userInfo{}, &httpStatusError{status: res.StatusCode, url: baseURL + "/api/v4/user"}
		}
		var u struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Name     string `json:"name"`
			Email    string `json:"email"`
		}
		if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
			return userInfo{}, err
		}

		return userInfo{
			Subject:  strconv.FormatInt(u.ID, 10),
			Username: u.Username,
			Email:    u.Email,
			Name:     u.Name,
		}, nil
	}
}

// forgejoUserInfo returns a userInfoFn that talks to the
// Forgejo /api/v1/user endpoint. baseURL is required (the
// caller has already validated it via the provider's
// Validate method).
func forgejoUserInfo(baseURL string) func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
	return func(ctx context.Context, c *http.Client, tok *oauth2.Token) (userInfo, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/user", nil)
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		res, err := c.Do(req)
		if err != nil {
			return userInfo{}, err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return userInfo{}, &httpStatusError{status: res.StatusCode, url: baseURL + "/api/v1/user"}
		}
		var u struct {
			ID       int64  `json:"id"`
			Login    string `json:"login"`
			FullName string `json:"full_name"`
			Email    string `json:"email"`
		}
		if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
			return userInfo{}, err
		}

		return userInfo{
			Subject:  strconv.FormatInt(u.ID, 10),
			Username: u.Login,
			Email:    u.Email,
			Name:     u.FullName,
		}, nil
	}
}

// httpStatusError is the error returned by userInfoFns on a
// non-200 response. Carrying the URL helps the operator
// debug a misconfigured self-hosted IdP.
type httpStatusError struct {
	status int
	url    string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("userinfo fetch %s returned HTTP %d", e.url, e.status)
}
