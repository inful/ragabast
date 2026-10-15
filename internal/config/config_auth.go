package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/gitlab"
)

// providerNameRegexp pins the URL-slug grammar for OAuthProvider.Name:
// lowercase letters, digits, and hyphens. The name is interpolated
// into /auth/<name>/callback paths so the strict ASCII constraint
// keeps the URL well-formed for any operator's reverse proxy.
var providerNameRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// reservedProviderNames are slugs the auth layer reserves for
// built-in endpoints (/auth/login, /auth/logout, /auth/me). A
// configured provider with one of these names would shadow a
// built-in route, so Validate rejects them.
var reservedProviderNames = map[string]bool{
	"login":  true,
	"logout": true,
	"me":     true,
}

// AuthConfig holds web-layer authentication settings. When
// Providers is non-empty, the web server runs an OAuth 2.0
// Authorization Code + PKCE flow per provider and accepts
// the resulting session cookie on protected routes. The
// bearer-token path (server.auth_token / server.auth_tokens)
// keeps working in parallel — programmatic API clients do
// not need a browser session to call the JSON API.
type AuthConfig struct {
	// SessionTTL is the maximum age of a session cookie
	// before it expires and the in-memory record is
	// evicted. Default 12h. A sliding-renewal middleware
	// extends the cookie on every authenticated request;
	// the TTL is the idle timeout. Set to 0 to disable
	// session expiry (not recommended for exposed
	// installs).
	SessionTTL time.Duration `env:"AUTH_SESSION_TTL" yaml:"session_ttl,omitempty"`

	// CookieName is the session-cookie name. Default
	// "ragabast_session". Custom names let operators run
	// multiple ragabast instances behind the same host
	// (different ports, different paths) without
	// colliding cookies, or align with an existing
	// cookie-allowlist.
	CookieName string `env:"AUTH_COOKIE_NAME" yaml:"cookie_name,omitempty"`

	// DefaultProviderName, when set, restricts the auth
	// surface to a single provider: the chooser page is
	// never rendered, /auth/login auto-redirects to the
	// default, and requests to non-default provider URLs
	// 404. Useful for multi-tenant deployments where
	// each instance is pinned to one IdP, or when an
	// operator wants to enforce "all sign-ins use this
	// provider" without exposing the others.
	//
	// Lives at the AuthConfig level (not on each provider)
	// because it's a property of the deployment, not of
	// the providers themselves — a single IdP can be
	// configured on many instances, but only some of
	// those instances may pin to it.
	//
	// Must match the `name:` field of one entry in
	// Providers (case-sensitive). Empty = no default;
	// multi-provider installs show the chooser as
	// before. Validate() surfaces "default set but no
	// matching provider" as a startup error so the
	// misconfiguration doesn't silently fall through to
	// the chooser.
	DefaultProviderName string `env:"AUTH_DEFAULT_PROVIDER_NAME" yaml:"default_provider_name,omitempty"`

	// Providers is the list of configured OAuth 2.0 /
	// OIDC identity providers. The order in this slice
	// is the order shown on the login page when more
	// than one provider is configured.
	Providers []OAuthProvider `env:"AUTH_PROVIDERS_JSON" yaml:"providers,omitempty"`
}

// OAuthProvider is one configured OAuth 2.0 / OIDC identity
// provider. The Type field selects the preset endpoint set
// (and, for oidc, signals the handler to run discovery at
// startup). Name is the URL slug under /auth/<name>/callback.
type OAuthProvider struct {
	// Name is the URL slug used in /auth/<name>/login
	// and /auth/<name>/callback. Must match
	// providerNameRegexp (lowercase ASCII letters,
	// digits, hyphens) and must not be one of the
	// reservedProviderNames ("login", "logout", "me").
	Name string `json:"name" yaml:"name"`

	// Type selects the endpoint preset. One of:
	//   "github"  — github.com OAuth app
	//   "gitlab"  — gitlab.com when BaseURL is empty,
	//               or any self-hosted GitLab when
	//               BaseURL is set
	//   "forgejo" — any Forgejo/Gitea instance;
	//               BaseURL is required (e.g.
	//               https://codeberg.org)
	//   "oidc"    — generic OpenID Connect; the
	//               handler runs discovery against
	//               DiscoveryURL at startup
	Type string `json:"type" yaml:"type"`

	// ClientID is the OAuth 2.0 client identifier
	// issued by the provider. Treated as
	// non-sensitive (it's a public identifier).
	ClientID string `json:"client_id" yaml:"client_id"`

	// ClientSecret is the OAuth 2.0 client secret.
	// Treated as sensitive — SaveConfig mode-0600's
	// the file; never log this field.
	ClientSecret string `json:"client_secret" yaml:"client_secret"`

	// BaseURL is the provider's root for self-hosted
	// presets. Used by type=gitlab (overrides the
	// gitlab.com default) and type=forgejo (required).
	// Ignored for type=github and type=oidc.
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty"`

	// DiscoveryURL is the OIDC issuer URL for
	// type=oidc (e.g. https://keycloak.example.com/realms/main).
	// NewProvider fetches the well-known configuration
	// from <DiscoveryURL>/.well-known/openid-configuration
	// at startup. Required when Type is "oidc"; ignored
	// otherwise.
	DiscoveryURL string `json:"discovery_url,omitempty" yaml:"discovery_url,omitempty"`

	// Scopes override the preset's default scope set.
	// Empty falls back to EffectiveScopes (which picks
	// the right defaults per Type).
	Scopes []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`

	// AllowedUsers is an optional allowlist. Match is
	// on the user's username (provider's `login` or
	// `username` field, depending on the provider) OR
	// email. Empty means any successfully
	// authenticated user from this provider is
	// accepted; non-empty is a strict whitelist.
	AllowedUsers []string `json:"allowed_users,omitempty" yaml:"allowed_users,omitempty"`

	// DefaultRole is the role assigned to
	// authenticated users from this provider. Empty
	// defaults to "user". Role mapping from IdP
	// claims (groups, teams) arrives in a later
	// change.
	DefaultRole string `json:"default_role,omitempty" yaml:"default_role,omitempty"`
}

// Validate reports configuration errors specific to this
// provider. Cross-provider checks (duplicate names) live
// on AuthConfig.Validate so they can be batched into the
// single Validate() error list.
func (p OAuthProvider) Validate() error {
	if p.Name == "" {
		return errors.New("auth.providers[].name is required")
	}
	if !providerNameRegexp.MatchString(p.Name) {
		return fmt.Errorf("auth.providers[].name %q must match %s", p.Name, providerNameRegexp.String())
	}
	switch p.Type {
	case "github", "gitlab", "forgejo", "oidc":
	default:
		return fmt.Errorf("auth.providers[%q].type %q is not one of: github, gitlab, forgejo, oidc", p.Name, p.Type)
	}
	if p.ClientID == "" {
		return fmt.Errorf("auth.providers[%q].client_id is required", p.Name)
	}
	if p.ClientSecret == "" {
		return fmt.Errorf("auth.providers[%q].client_secret is required", p.Name)
	}
	if p.Type == "forgejo" && p.BaseURL == "" {
		return fmt.Errorf("auth.providers[%q].base_url is required for type=forgejo", p.Name)
	}
	if p.Type == "oidc" && p.DiscoveryURL == "" {
		return fmt.Errorf("auth.providers[%q].discovery_url is required for type=oidc", p.Name)
	}

	return nil
}

// Endpoint returns the OAuth 2.0 endpoint set this provider
// speaks. The static presets (github, gitlab, forgejo) all
// have a hard-coded AuthURL+TokenURL pair. OIDC providers
// return an error here because their endpoint is discovered
// at runtime — callers that need it should use RequiresDiscovery
// to dispatch into the go-oidc.NewProvider path instead.
func (p OAuthProvider) Endpoint() (oauth2.Endpoint, error) {
	switch p.Type {
	case "github":
		return github.Endpoint, nil
	case "gitlab":
		if p.BaseURL == "" {
			return gitlab.Endpoint, nil
		}

		base := strings.TrimRight(p.BaseURL, "/")

		return oauth2.Endpoint{
			AuthURL:  base + "/oauth/authorize",
			TokenURL: base + "/oauth/token",
		}, nil
	case "forgejo":
		if p.BaseURL == "" {
			return oauth2.Endpoint{}, fmt.Errorf("auth.providers[%q].base_url is required for type=forgejo", p.Name)
		}

		base := strings.TrimRight(p.BaseURL, "/")

		// Forgejo speaks the GitHub-compatible OAuth shape:
		// /login/oauth/authorize and /login/oauth/access_token.
		return oauth2.Endpoint{
			AuthURL:  base + "/login/oauth/authorize",
			TokenURL: base + "/login/oauth/access_token",
		}, nil
	case "oidc":
		return oauth2.Endpoint{}, fmt.Errorf("auth.providers[%q] type=oidc requires discovery_url and runtime OIDC discovery; use RequiresDiscovery to dispatch", p.Name)
	default:
		return oauth2.Endpoint{}, fmt.Errorf("auth.providers[%q] has unknown type %q", p.Name, p.Type)
	}
}

// RequiresDiscovery reports whether the provider needs the
// go-oidc discovery handshake at startup. Only type=oidc
// requires it — the rest have static endpoint sets and
// skip the discovery round-trip.
func (p OAuthProvider) RequiresDiscovery() bool {
	return p.Type == "oidc"
}

// EffectiveScopes returns the OAuth scopes this provider
// will request. If Scopes is set, it wins (operator
// override). Otherwise the per-type preset default is
// returned.
//
// The defaults are chosen to give the callback handler
// enough to identify the user without asking for more
// than the IdP's basic profile.
//
//   - github:  read:user + user:email — the minimal set
//     that returns a stable user id and a verified email.
//   - gitlab:  read_user + profile + email — read_user
//     is the GitLab API scope that grants /api/v4/user
//     access. Without it, GitLab returns HTTP 403 on
//     the userinfo fetch regardless of token validity.
//     profile and email are OIDC claims GitLab populates
//     on the /oauth/userinfo endpoint when read_user is
//     granted. openid is intentionally NOT in the default
//     set — it's only honored on GitLab 16.0+ with OIDC
//     applications explicitly enabled, and on older Gitlabs
//     requesting it produces a confusing login failure.
//     Operators on OIDC-enabled GitLab who want the
//     ID-token path should switch to type=oidc with a
//     discovery_url instead.
//   - forgejo: read:user + user:email — matches the
//     GitHub-compatible Forgejo OAuth shape.
//   - oidc:    openid + profile + email — required by
//     the OIDC spec for ID-token issuance. type=oidc
//     providers always go through the OIDC UserInfo
//     claim flow, so these are sufficient.
func (p OAuthProvider) EffectiveScopes() []string {
	if len(p.Scopes) > 0 {
		out := make([]string, len(p.Scopes))
		copy(out, p.Scopes)

		return out
	}
	switch p.Type {
	case "github", "forgejo":
		return []string{"read:user", "user:email"}
	case "gitlab":
		// read_user is the GitLab API scope that
		// unlocks /api/v4/user. profile and email
		// are the OIDC claims GitLab populates
		// alongside it. openid is omitted here on
		// purpose — see the docstring above.
		return []string{"read_user", "profile", "email"}
	case "oidc":
		return []string{"openid", "profile", "email"}
	default:
		return []string{"openid", "profile", "email"}
	}
}

// RedirectURL is the callback URL the IdP will redirect to.
// serverBase is the externally-reachable origin of the
// ragabast server (scheme + host [+ port]); the per-provider
// path is appended. serverBase should NOT have a trailing
// slash — RedirectURL appends "/auth/<name>/callback".
func (p OAuthProvider) RedirectURL(serverBase string) string {
	base := strings.TrimRight(serverBase, "/")

	return base + "/auth/" + p.Name + "/callback"
}

// validateAuth runs the cross-provider validation: each
// provider validates itself, then we check for duplicate
// names, reserved-name collisions, and — when
// DefaultProviderName is set — that the default matches a
// configured provider. Returned error is a single combined
// message suitable for Validate()'s errs list (joined with
// newlines).
func (c *Config) validateAuth() error {
	if len(c.Auth.Providers) == 0 {
		if c.Auth.DefaultProviderName != "" {
			return fmt.Errorf("auth.default_provider_name %q is set but no providers are configured", c.Auth.DefaultProviderName)
		}

		return nil
	}
	seen := make(map[string]bool, len(c.Auth.Providers))

	var errs []string
	for i, p := range c.Auth.Providers {
		if err := p.Validate(); err != nil {
			errs = append(errs, err.Error())
		}
		if reservedProviderNames[p.Name] {
			errs = append(errs, fmt.Sprintf("auth.providers[%d].name %q is reserved (login, logout, me are built-in routes)", i, p.Name))
		}
		if seen[p.Name] {
			errs = append(errs, fmt.Sprintf("auth.providers[%d].name %q is a duplicate of an earlier entry", i, p.Name))
		}
		seen[p.Name] = true
	}

	// Validate the default-provider pin AFTER the per-provider
	// checks so we don't report a "default doesn't match" error
	// for a config that has a more fundamental problem.
	if c.Auth.DefaultProviderName != "" {
		if !seen[c.Auth.DefaultProviderName] {
			errs = append(errs, fmt.Sprintf(
				"auth.default_provider_name %q does not match any configured provider (available: %s)",
				c.Auth.DefaultProviderName,
				availableProviderNames(c.Auth.Providers),
			))
		}
	}

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

// availableProviderNames returns a sorted, quoted list of
// provider names. Used to make "default_provider_name points
// at nothing" errors actionable — the operator sees the
// actual valid options in the error message rather than
// having to scroll back through their config to remember
// what they named everything.
func availableProviderNames(providers []OAuthProvider) string {
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, strconv.Quote(p.Name))
	}

	return strings.Join(names, ", ")
}
