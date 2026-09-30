package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestAuthConfig_DefaultsAreEmpty pins the "no auth is the default"
// invariant: a fresh DefaultConfig has zero OAuth providers, zero
// session TTL, and a blank cookie name. The web layer treats these
// as "session auth disabled; bearer token (or open access) is the
// only auth path".
func TestAuthConfig_DefaultsAreEmpty(t *testing.T) {
	cfg := DefaultConfig()

	assert.Empty(t, cfg.Auth.Providers)
	assert.Equal(t, time.Duration(0), cfg.Auth.SessionTTL)
	assert.Empty(t, cfg.Auth.CookieName)
	assert.Empty(t, cfg.Auth.DefaultProviderName,
		"default_provider_name must default to empty so single-provider installs don't accidentally pin")
}

func TestOAuthProvider_ValidateRequiresName(t *testing.T) {
	p := OAuthProvider{Type: "github", ClientID: "id", ClientSecret: "sec"}

	err := p.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestOAuthProvider_ValidateRequiresClientCredentials(t *testing.T) {
	p := OAuthProvider{Name: "gh", Type: "github"}

	err := p.Validate()

	require.Error(t, err)
}

func TestOAuthProvider_ValidateRejectsUnknownType(t *testing.T) {
	p := OAuthProvider{Name: "weird", Type: "myspace", ClientID: "id", ClientSecret: "sec"}

	err := p.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "type")
}

func TestOAuthProvider_EndpointGitHub(t *testing.T) {
	p := OAuthProvider{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"}

	ep, err := p.Endpoint()

	require.NoError(t, err)
	assert.Equal(t, "https://github.com/login/oauth/authorize", ep.AuthURL)
	assert.Equal(t, "https://github.com/login/oauth/access_token", ep.TokenURL)
}

func TestOAuthProvider_EndpointGitLabCom(t *testing.T) {
	p := OAuthProvider{Name: "gl", Type: "gitlab", ClientID: "id", ClientSecret: "sec"}

	ep, err := p.Endpoint()

	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.com/oauth/authorize", ep.AuthURL)
	assert.Equal(t, "https://gitlab.com/oauth/token", ep.TokenURL)
}

func TestOAuthProvider_EndpointGitLabSelfHosted(t *testing.T) {
	p := OAuthProvider{Name: "gl", Type: "gitlab", ClientID: "id", ClientSecret: "sec", BaseURL: "https://gl.example.com"}

	ep, err := p.Endpoint()

	require.NoError(t, err)
	assert.Equal(t, "https://gl.example.com/oauth/authorize", ep.AuthURL)
	assert.Equal(t, "https://gl.example.com/oauth/token", ep.TokenURL)
}

func TestOAuthProvider_EndpointForgejoRequiresBaseURL(t *testing.T) {
	p := OAuthProvider{Name: "fj", Type: "forgejo", ClientID: "id", ClientSecret: "sec"}

	_, err := p.Endpoint()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

func TestOAuthProvider_EndpointForgejoWithBaseURL(t *testing.T) {
	p := OAuthProvider{Name: "fj", Type: "forgejo", ClientID: "id", ClientSecret: "sec", BaseURL: "https://codeberg.org"}

	ep, err := p.Endpoint()

	require.NoError(t, err)
	// Forgejo speaks the GitHub-compatible OAuth shape: /login/oauth/{authorize,access_token}.
	assert.Equal(t, "https://codeberg.org/login/oauth/authorize", ep.AuthURL)
	assert.Equal(t, "https://codeberg.org/login/oauth/access_token", ep.TokenURL)
}

func TestOAuthProvider_EndpointOIDCRequiresDiscoveryURL(t *testing.T) {
	p := OAuthProvider{Name: "oidc", Type: "oidc", ClientID: "id", ClientSecret: "sec"}

	_, err := p.Endpoint()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "discovery_url")
}

func TestOAuthProvider_RequiresDiscovery(t *testing.T) {
	assert.False(t, (&OAuthProvider{Type: "github"}).RequiresDiscovery())
	assert.False(t, (&OAuthProvider{Type: "gitlab"}).RequiresDiscovery())
	assert.False(t, (&OAuthProvider{Type: "forgejo"}).RequiresDiscovery())
	assert.True(t, (&OAuthProvider{Type: "oidc"}).RequiresDiscovery())
}

func TestOAuthProvider_DefaultScopesForGitHub(t *testing.T) {
	p := OAuthProvider{Type: "github", ClientID: "id", ClientSecret: "sec"}

	assert.Equal(t, []string{"read:user", "user:email"}, p.EffectiveScopes())
}

func TestOAuthProvider_DefaultScopesForGitLab(t *testing.T) {
	p := OAuthProvider{Type: "gitlab", ClientID: "id", ClientSecret: "sec"}

	// read_user is the GitLab API scope that unlocks
	// /api/v4/user. Without it, GitLab returns HTTP 403 on
	// the userinfo fetch regardless of token validity —
	// the v0.9.0 default of [openid, profile, email] caused
	// exactly this failure in production. openid is
	// intentionally omitted because only GitLab 16.0+
	// with OIDC applications explicitly enabled honors it;
	// including it on older GitLabs produces a confusing
	// login failure. Operators who want the OIDC ID-token
	// path should switch to type=oidc with a discovery_url.
	assert.Equal(t, []string{"read_user", "profile", "email"}, p.EffectiveScopes())
}

func TestOAuthProvider_DefaultScopesForForgejo(t *testing.T) {
	p := OAuthProvider{Type: "forgejo", ClientID: "id", ClientSecret: "sec", BaseURL: "https://codeberg.org"}

	assert.Equal(t, []string{"read:user", "user:email"}, p.EffectiveScopes())
}

func TestOAuthProvider_DefaultScopesForOIDC(t *testing.T) {
	p := OAuthProvider{Type: "oidc", ClientID: "id", ClientSecret: "sec"}

	assert.Equal(t, []string{"openid", "profile", "email"}, p.EffectiveScopes())
}

func TestOAuthProvider_EffectiveScopesOverride(t *testing.T) {
	p := OAuthProvider{Type: "github", ClientID: "id", ClientSecret: "sec", Scopes: []string{"repo"}}

	assert.Equal(t, []string{"repo"}, p.EffectiveScopes())
}

// TestOAuthProvider_GitLabDefaultScopesIncludeReadUser pins
// the v0.9.0→v0.10.3 fix for the GitLab 403 on userinfo
// fetch. The default GitLab scope set MUST include
// `read_user`, because:
//
//  1. ragabast fetches userinfo via GET /api/v4/user
//     with the access token in the Authorization header.
//  2. GitLab enforces scope-level access on that endpoint
//     — a token without read_user (or api / read_api) gets
//     HTTP 403 even if it's otherwise valid.
//  3. The historical default [openid, profile, email] was
//     OIDC-shaped and didn't include read_user, so every
//     GitLab login (gitlab.com + self-hosted) failed at
//     the userinfo step.
//
// This test exists so a future "let's clean up the scopes"
// PR can't silently drop read_user and reintroduce the 403.
// Operators who want a different scope set can override
// via Scopes; the default just needs to give the callback
// handler enough to talk to /api/v4/user successfully.
func TestOAuthProvider_GitLabDefaultScopesIncludeReadUser(t *testing.T) {
	t.Helper()

	p := OAuthProvider{Type: "gitlab", ClientID: "id", ClientSecret: "sec"}

	scopes := p.EffectiveScopes()
	assert.Contains(t, scopes, "read_user",
		"GitLab default scopes must include read_user so /api/v4/user returns 200; without it GitLab returns HTTP 403 regardless of token validity")
}

func TestConfig_RoundTripsAuthConfigYAML(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = []OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec", AllowedUsers: []string{"alice"}},
		{Name: "gl", Type: "gitlab", ClientID: "id2", ClientSecret: "sec2", BaseURL: "https://gl.example.com"},
	}

	data, err := yaml.Marshal(cfg)

	require.NoError(t, err)

	var loaded Config

	require.NoError(t, yaml.Unmarshal(data, &loaded))

	require.Len(t, loaded.Auth.Providers, 2)
	assert.Equal(t, "gh", loaded.Auth.Providers[0].Name)
	assert.Equal(t, "github", loaded.Auth.Providers[0].Type)
	assert.Equal(t, []string{"alice"}, loaded.Auth.Providers[0].AllowedUsers)
	assert.Equal(t, "https://gl.example.com", loaded.Auth.Providers[1].BaseURL)
}

func TestConfig_ValidateAcceptsEmptyAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = nil

	assert.NoError(t, cfg.Validate())
}

func TestConfig_ValidateAcceptsValidProviders(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = []OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
	}

	assert.NoError(t, cfg.Validate())
}

func TestConfig_ValidateRejectsDuplicateProviderNames(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = []OAuthProvider{
		{Name: "dup", Type: "github", ClientID: "id", ClientSecret: "sec"},
		{Name: "dup", Type: "gitlab", ClientID: "id2", ClientSecret: "sec2"},
	}

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestConfig_ValidateRejectsReservedProviderNames(t *testing.T) {
	for _, reserved := range []string{"login", "logout", "me"} {
		t.Run(reserved, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.Providers = []OAuthProvider{
				{Name: reserved, Type: "github", ClientID: "id", ClientSecret: "sec"},
			}

			err := cfg.Validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "reserved")
		})
	}
}

func TestConfig_ValidateRejectsProviderNameWithBadCharacters(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = []OAuthProvider{
		{Name: "has spaces", Type: "github", ClientID: "id", ClientSecret: "sec"},
	}

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestConfig_ApplyEnvOverrides_AuthProvidersFromJSON(t *testing.T) {
	t.Setenv("AUTH_PROVIDERS_JSON", `[{"name":"env-gh","type":"github","client_id":"id","client_secret":"sec"}]`)

	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()

	require.Len(t, cfg.Auth.Providers, 1)
	assert.Equal(t, "env-gh", cfg.Auth.Providers[0].Name)
	assert.Equal(t, "github", cfg.Auth.Providers[0].Type)
	assert.Equal(t, "id", cfg.Auth.Providers[0].ClientID)
	assert.Equal(t, "sec", cfg.Auth.Providers[0].ClientSecret)
}

func TestConfig_ApplyEnvOverrides_AuthSessionTTL(t *testing.T) {
	t.Setenv("AUTH_SESSION_TTL", "30m")

	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()

	assert.Equal(t, 30*time.Minute, cfg.Auth.SessionTTL)
}

func TestConfig_ApplyEnvOverrides_AuthCookieName(t *testing.T) {
	t.Setenv("AUTH_COOKIE_NAME", "custom_session")

	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()

	assert.Equal(t, "custom_session", cfg.Auth.CookieName)
}

// TestConfig_ApplyEnvOverrides_AuthDefaultProviderName pins the
// env-var wiring: AUTH_DEFAULT_PROVIDER_NAME populates
// AuthConfig.DefaultProviderName. Without this override the
// default stays empty, which is the historical behavior.
func TestConfig_ApplyEnvOverrides_AuthDefaultProviderName(t *testing.T) {
	t.Setenv("AUTH_DEFAULT_PROVIDER_NAME", "work-gitlab")

	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()

	assert.Equal(t, "work-gitlab", cfg.Auth.DefaultProviderName)
}

// TestValidate_DefaultProviderNameMatchesConfiguredProvider
// pins the happy path: setting default_provider_name to a
// real provider name passes Validate. The server picks the
// named provider up at startup via newOAuthHandlers.
func TestValidate_DefaultProviderNameMatchesConfiguredProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = []OAuthProvider{
		{Name: "github", Type: "github", ClientID: "id", ClientSecret: "sec"},
		{Name: "work-gitlab", Type: "gitlab", ClientID: "id2", ClientSecret: "sec2", BaseURL: "https://gitlab.work"},
	}
	cfg.Auth.DefaultProviderName = "work-gitlab"

	assert.NoError(t, cfg.Validate())
}

// TestValidate_DefaultProviderNameDoesNotMatchConfiguredProvider
// pins the failure mode: setting default_provider_name to a
// typo (or a stale name from a removed provider) fails
// Validate with an error that names the available providers
// so the operator can fix it without grepping their config.
func TestValidate_DefaultProviderNameDoesNotMatchConfiguredProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = []OAuthProvider{
		{Name: "github", Type: "github", ClientID: "id", ClientSecret: "sec"},
		{Name: "work-gitlab", Type: "gitlab", ClientID: "id2", ClientSecret: "sec2", BaseURL: "https://gitlab.work"},
	}
	cfg.Auth.DefaultProviderName = "work-glitlab" // note the typo

	require.Error(t, cfg.Validate())
	errMsg := cfg.Validate().Error()
	assert.Contains(t, errMsg, "work-glitlab",
		"error must name the bad default so the operator sees what they typo'd")
	assert.Contains(t, errMsg, "available",
		"error must point at the available providers list")
	assert.Contains(t, errMsg, "github",
		"error must list at least one of the valid providers")
}

// TestValidate_DefaultProviderNameButNoProviders pins the
// edge case: setting default without configuring any
// providers fails with a clear message. Without this guard,
// newOAuthHandlers would silently produce an empty providers
// map and every auth route would 404.
func TestValidate_DefaultProviderNameButNoProviders(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = nil
	cfg.Auth.DefaultProviderName = "anything"

	require.Error(t, cfg.Validate())
	assert.Contains(t, cfg.Validate().Error(), "no providers are configured")
}

// TestValidate_DefaultProviderNameEmptyIsOK pins the historical
// behavior: empty default means "no pin, show the chooser if
// multiple providers, auto-redirect if one". Multi-provider
// installs with no default still pass Validate.
func TestValidate_DefaultProviderNameEmptyIsOK(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Providers = []OAuthProvider{
		{Name: "github", Type: "github", ClientID: "id", ClientSecret: "sec"},
		{Name: "work-gitlab", Type: "gitlab", ClientID: "id2", ClientSecret: "sec2", BaseURL: "https://gitlab.work"},
	}
	cfg.Auth.DefaultProviderName = ""

	assert.NoError(t, cfg.Validate())
}

// TestOAuthProvider_RedirectURL pins the redirect-URL derivation:
// every provider exposes its callback under /auth/<name>/callback,
// relative to the server's externally-reachable URL. Tests don't
// pin the server URL (that's a runtime choice) — only the path
// suffix and the URL-safe construction.
func TestOAuthProvider_RedirectURL(t *testing.T) {
	p := OAuthProvider{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"}

	got := p.RedirectURL("https://ragabast.example.com")

	assert.Equal(t, "https://ragabast.example.com/auth/gh/callback", got)
}
