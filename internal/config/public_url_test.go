package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplyEnvOverrides_PublicURL pins the env-var wiring
// for the new knob: SERVER_PUBLIC_URL populates
// ServerConfig.PublicURL. The override exists so
// Kubernetes operators behind Traefik / nginx / a
// managed L7 LB can register an OAuth callback URL
// that matches their public-facing hostname without
// having to rebind the listener to that hostname too.
func TestApplyEnvOverrides_PublicURL(t *testing.T) {
	t.Setenv("SERVER_PUBLIC_URL", "https://ragabast.example.com")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, "https://ragabast.example.com",
		cfg.Server.PublicURL,
		"SERVER_PUBLIC_URL must populate ServerConfig.PublicURL")
}

// TestApplyEnvOverrides_PublicURLDefaultsEmpty pins the
// unconfigured behavior: when SERVER_PUBLIC_URL is
// unset, the field stays empty so deriveServerBase
// falls back to the historical address+port derivation.
// Existing operators see no change.
func TestApplyEnvOverrides_PublicURLDefaultsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Empty(t, cfg.Server.PublicURL,
		"missing SERVER_PUBLIC_URL must leave the field empty")
}

// TestDefaultConfig_PublicURLEmpty pins the direct-from-
// DefaultConfig behavior (env override path not
// exercised). Same intent — empty by default — but at
// a different config layer so a future refactor that
// wires a default doesn't slip through.
func TestDefaultConfig_PublicURLEmpty(t *testing.T) {
	cfg := DefaultConfig()
	require.Empty(t, cfg.Server.PublicURL,
		"default config must not pre-fill server.public_url")
}

// TestValidate_PublicURLAcceptsHTTPS pins the happy
// path: a valid https URL passes Validate without
// touching the other server fields.
func TestValidate_PublicURLAcceptsHTTPS(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.PublicURL = "https://ragabast.example.com"
	require.NoError(t, cfg.Validate())
}

// TestValidate_PublicURLAcceptsHTTP pins the second
// happy path: a valid http URL (the common k8s ingress
// setup where TLS is terminated by the proxy) passes
// Validate. We don't pin a scheme preference — both
// are legitimate, and the right one depends on what
// the IdP was registered with.
func TestValidate_PublicURLAcceptsHTTP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.PublicURL = "http://ragabast.internal:8080"
	require.NoError(t, cfg.Validate())
}

// TestValidate_PublicURLRejectsInvalidURL pins the
// failure mode: a URL the parser can't make sense of
// fails Validate with a clear message naming the
// field. Operators see "server.public_url is not a
// valid URL: <parser error>" rather than a generic
// 500 at startup.
func TestValidate_PublicURLRejectsInvalidURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.PublicURL = "ht!tp://broken url with spaces"

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "server.public_url")
}

// TestValidate_PublicURLRejectsNonHTTPScheme pins the
// scheme guard: anything other than http/https is
// refused with a clear error. file://, ws://, ftp://
// and friends would all silently break OAuth (the
// IdP would reject the redirect target) so we catch
// them at config time.
func TestValidate_PublicURLRejectsNonHTTPScheme(t *testing.T) {
	cases := []string{
		"ftp://ragabast.example.com",
		"file:///etc/passwd",
		"ws://ragabast.example.com",
		"javascript:alert(1)",
		"example.com/no-scheme", // parses with empty scheme; not http/https
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Server.PublicURL = raw
			err := cfg.Validate()
			require.Error(t, err, "scheme should be rejected for %q", raw)
			require.Contains(t, err.Error(), "server.public_url")
		})
	}
}

// TestValidate_PublicURLRejectsEmptyHost pins the
// host guard: a URL that parses but has no host (e.g.
// "https://" or "https:///path") fails Validate.
// Without a host the callback URL would be unrouteable
// and the IdP would reject the redirect.
func TestValidate_PublicURLRejectsEmptyHost(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.PublicURL = "https://"

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "host")
}
