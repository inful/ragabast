package web

import (
	"context"
	"testing"
	"time"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildProviderEntry_GitHub pins the github branch's
// config wiring: hard-coded `https://api.github.com`
// userinfo URL, no discovery, scopes default to
// read:user + user:email.
func TestBuildProviderEntry_GitHub(t *testing.T) {
	t.Parallel()

	prov := config.OAuthProvider{
		Name:         "gh",
		Type:         "github",
		ClientID:     "id",
		ClientSecret: "sec",
	}

	entry, err := buildProviderEntry(
		context.Background(), prov, "https://app.example.com")
	require.NoError(t, err)
	require.NotNil(t, entry)

	assert.Equal(t, "https://app.example.com/auth/gh/callback",
		entry.oauth2Config.RedirectURL)
	assert.Contains(t, entry.oauth2Config.Scopes, "read:user")
	assert.Contains(t, entry.oauth2Config.Scopes, "user:email")
	// GitHub has a known-stable AuthURL; check the well-known
	// prefix rather than the full URL (which evolves).
	assert.Contains(t, entry.oauth2Config.Endpoint.AuthURL, "github.com")
}

// TestBuildProviderEntry_GitLab_DefaultsToGitlabCom pins the
// empty-BaseURL fallback: a type=gitlab provider with no
// BaseURL routes to https://gitlab.com (the public SaaS).
func TestBuildProviderEntry_GitLab_DefaultsToGitlabCom(t *testing.T) {
	t.Parallel()

	prov := config.OAuthProvider{
		Name: "gl", Type: "gitlab",
		ClientID: "id", ClientSecret: "sec",
		// BaseURL intentionally empty.
	}

	entry, err := buildProviderEntry(
		context.Background(), prov, "https://app.example.com")
	require.NoError(t, err)
	require.NotNil(t, entry)

	assert.Contains(t, entry.oauth2Config.Endpoint.AuthURL, "gitlab.com")
	// Scopes must include read_user — without it, /api/v4/user
	// returns 403 regardless of token validity (documented in
	// the README and in config_auth.go's EffectiveScopes).
	assert.Contains(t, entry.oauth2Config.Scopes, "read_user")
}

// TestBuildProviderEntry_GitLab_SelfHosted pins the BaseURL
// override: a self-hosted GitLab is identified by its BaseURL.
func TestBuildProviderEntry_GitLab_SelfHosted(t *testing.T) {
	t.Parallel()

	prov := config.OAuthProvider{
		Name: "gl-selfhost", Type: "gitlab",
		ClientID: "id", ClientSecret: "sec",
		BaseURL: "https://gitlab.example.com",
	}

	entry, err := buildProviderEntry(
		context.Background(), prov, "https://app.example.com")
	require.NoError(t, err)
	require.NotNil(t, entry)

	assert.Contains(t, entry.oauth2Config.Endpoint.AuthURL,
		"gitlab.example.com",
		"self-hosted GitLab must use the BaseURL, not the gitlab.com default")
	assert.NotContains(t, entry.oauth2Config.Endpoint.AuthURL,
		"gitlab.com",
		"BaseURL must override the public-SaaS default")
}

// TestBuildProviderEntry_Forgejo_SelfHosted pins the
// forgejo branch: BaseURL is REQUIRED (unlike gitlab which
// defaults to gitlab.com). Validate() should have already
// caught this, but the factory still defaults to BaseURL
// when constructing the userInfo URL.
func TestBuildProviderEntry_Forgejo_SelfHosted(t *testing.T) {
	t.Parallel()

	prov := config.OAuthProvider{
		Name: "fj", Type: "forgejo",
		ClientID: "id", ClientSecret: "sec",
		BaseURL: "https://codeberg.org",
	}

	entry, err := buildProviderEntry(
		context.Background(), prov, "https://app.example.com")
	require.NoError(t, err)
	require.NotNil(t, entry)

	assert.Contains(t, entry.oauth2Config.Endpoint.AuthURL, "codeberg.org")
}

// TestBuildProviderEntry_OIDC_RunsDiscovery pins the OIDC
// branch: discovery fires against DiscoveryURL, the
// returned oauth2.Endpoint replaces the static one.
// Uses a httptest server that serves the discovery doc.
func TestBuildProviderEntry_OIDC_RunsDiscovery(t *testing.T) {
	t.Parallel()

	user := fakeUser{ID: 9, Username: "bob", Name: "Bob", Email: "bob@x.com"}
	srv := fakeOIDCServer(t, "client", user)
	defer srv.Close()

	prov := config.OAuthProvider{
		Name:         "oidc",
		Type:         "oidc",
		ClientID:     "client",
		ClientSecret: "sec",
		DiscoveryURL: srv.URL,
	}

	entry, err := buildProviderEntry(
		context.Background(), prov, "https://app.example.com")
	require.NoError(t, err)
	require.NotNil(t, entry)

	assert.Equal(t, srv.URL+"/token", entry.oauth2Config.Endpoint.TokenURL)
}

// TestBuildProviderEntry_OIDC_BadDiscoveryURL returns the
// underlying discovery error verbatim so operators see a
// useful message at startup instead of "every auth route
// 404s" at runtime.
func TestBuildProviderEntry_OIDC_BadDiscoveryURL(t *testing.T) {
	t.Parallel()

	prov := config.OAuthProvider{
		Name:         "oidc",
		Type:         "oidc",
		ClientID:     "client",
		ClientSecret: "sec",
		DiscoveryURL: "http://127.0.0.1:1/no-such-server",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := buildProviderEntry(ctx, prov, "https://app.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OIDC discovery",
		"the error message must point at the failure mode for the operator")
}

// TestBuildProviderEntry_UnsupportedType pins the error
// path for a provider with a Type that's not in the
// supported set. The function should return an error
// rather than silently registering nothing.
func TestBuildProviderEntry_UnsupportedType(t *testing.T) {
	t.Parallel()

	prov := config.OAuthProvider{
		Name: "weird", Type: "twitter",
		ClientID: "id", ClientSecret: "sec",
	}

	_, err := buildProviderEntry(
		context.Background(), prov, "https://app.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "twitter",
		"the error must mention the bad type for the operator")
}
