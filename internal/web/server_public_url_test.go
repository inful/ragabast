package web

import (
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestDeriveServerBase_PublicURLWins pins the
// reverse-proxy escape hatch: when an operator sets
// server.public_url, the derived callback origin is
// exactly that URL (with a trimmed trailing slash),
// regardless of server.address / server.port. This
// is the Traefik / nginx / L7-LB case.
func TestDeriveServerBase_PublicURLWins(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Address:   "0.0.0.0",
			Port:      8080,
			PublicURL: "https://ragabast.example.com",
		},
	}
	require.Equal(t, "https://ragabast.example.com", deriveServerBase(cfg))
}

// TestDeriveServerBase_PublicURLTrimsTrailingSlash pins
// the concatenation safety: a stray trailing slash on
// public_url must not produce `<URL>//auth/<name>/callback`
// when concatenated with the callback path.
func TestDeriveServerBase_PublicURLTrimsTrailingSlash(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.ServerConfig{
			PublicURL: "https://ragabast.example.com/",
		},
	}
	require.Equal(t, "https://ragabast.example.com", deriveServerBase(cfg),
		"trailing slash must be trimmed to avoid double-slash in callback URLs")
}

// TestDeriveServerBase_PublicURLSupportsSubpath pins the
// reverse-proxy subpath case: an operator mounting
// ragabast at https://example.com/ragabast needs the
// callback URL to carry the subpath, otherwise the
// proxy won't route the IdP's redirect back. Concatenating
// /auth/<name>/callback after a subpath-bearing origin
// produces the right URL because RedirectURL just glues
// the paths together.
func TestDeriveServerBase_PublicURLSupportsSubpath(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.ServerConfig{
			PublicURL: "https://example.com/ragabast",
		},
	}
	require.Equal(t, "https://example.com/ragabast", deriveServerBase(cfg))
}

// TestDeriveServerBase_FallsBackToAddressPort pins the
// historical behavior: when public_url is empty,
// deriveServerBase returns http://<address>:<port>
// using server.address and server.port. This is the
// localhost-dev / single-host case. Existing operators
// without public_url see no change.
func TestDeriveServerBase_FallsBackToAddressPort(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Address: "127.0.0.1",
			Port:    9000,
		},
	}
	require.Equal(t, "http://127.0.0.1:9000", deriveServerBase(cfg))
}

// TestDeriveServerBase_DefaultsWhenFieldsEmpty pins the
// historical behavior of deriveServerBase's defaults:
// Address="" falls back to "localhost", Port=0 falls
// back to 8080. Both defaults match ServerConfig.ListenAddr
// so the bind URL and the OAuth callback URL agree for
// a default install.
func TestDeriveServerBase_DefaultsWhenFieldsEmpty(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.ServerConfig{},
	}
	require.Equal(t, "http://localhost:8080", deriveServerBase(cfg))
}

// TestDeriveServerBase_StripsPortFromAddress pins the
// corner case where server.address carries a port
// (e.g. ":8080" or "0.0.0.0:8080"). deriveServerBase
// strips the port and re-appends server.port so the
// scheme://host:port string is always well-formed.
func TestDeriveServerBase_StripsPortFromAddress(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Address: "0.0.0.0:9999",
			Port:    8080,
		},
	}
	require.Equal(t, "http://0.0.0.0:8080", deriveServerBase(cfg))
}

// TestDeriveServerBase_TrimsWhitespaceAroundPublicURL
// pins the env-var safety: shell expansion or copy-paste
// sometimes introduces leading/trailing whitespace. We
// strip it so a public_url like "  https://x.example.com "
// still resolves correctly instead of producing an
// awkward URL like "  https://x.example.com/auth/...".
func TestDeriveServerBase_TrimsWhitespaceAroundPublicURL(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.ServerConfig{
			PublicURL: "  https://ragabast.example.com  ",
		},
	}
	require.Equal(t, "https://ragabast.example.com", deriveServerBase(cfg))
}
