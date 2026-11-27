package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWebIngest_IsHostAllowed pins the operator's boundary: a
// document can only cause a fetch of a host on the allowlist.
//
// This is the check that keeps a document from turning ragabast
// into a request proxy: the request is the author's decision, the
// allowance is the operator's.
func TestWebIngest_IsHostAllowed(t *testing.T) {
	cfg := &WebIngestConfig{AllowedHosts: []string{"example.com", "semver.org:8080"}}

	require.True(t, cfg.IsHostAllowed("example.com"), "a listed host is allowed")
	require.True(t, cfg.IsHostAllowed("EXAMPLE.COM"), "the comparison is case-insensitive")
	require.True(t, cfg.IsHostAllowed("example.com:9999"), "a bare host allows any port")
	require.True(t, cfg.IsHostAllowed("semver.org:8080"), "a host:port entry allows that port")

	require.False(t, cfg.IsHostAllowed("semver.org"), "a host:port entry does not allow other ports")
	require.False(t, cfg.IsHostAllowed("evil.test"), "an unlisted host is refused")
	require.False(t, cfg.IsHostAllowed(""), "an empty host is refused")
}

// TestWebIngest_IsURLAllowed pins the URL-level check: http and
// https only, on an allowed host.
func TestWebIngest_IsURLAllowed(t *testing.T) {
	cfg := &WebIngestConfig{AllowedHosts: []string{"example.com"}}

	require.True(t, cfg.IsURLAllowed("https://example.com/spec"))
	require.True(t, cfg.IsURLAllowed("http://example.com/spec"))

	require.False(t, cfg.IsURLAllowed("file:///etc/passwd"), "only http and https")
	require.False(t, cfg.IsURLAllowed("https://evil.test/spec"), "the host must be allowed")
	require.False(t, cfg.IsURLAllowed("not a url"))
}

// TestWebIngest_EmptyAllowlistMeansOff pins that the feature is
// off until the operator turns it on: an empty allowlist allows
// nothing.
func TestWebIngest_EmptyAllowlistMeansOff(t *testing.T) {
	cfg := &WebIngestConfig{}

	require.False(t, cfg.IsHostAllowed("example.com"))
	require.False(t, cfg.IsURLAllowed("https://example.com/spec"))
	require.NoError(t, cfg.Validate(), "an empty allowlist is valid: the feature is off")
}

// TestWebIngest_ValidateRejectsURLsInTheAllowlist pins that the
// allowlist holds hosts, not URLs — a URL there would silently
// never match, because the comparison is on the host.
func TestWebIngest_ValidateRejectsURLsInTheAllowlist(t *testing.T) {
	cfg := &WebIngestConfig{AllowedHosts: []string{"https://example.com/spec"}}

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "allowed_hosts")
}

// TestApplyEnvOverrides_WebIngest pins the env wiring for the
// policy: the allowed hosts, the refresh interval, and the fetch
// bounds.
func TestApplyEnvOverrides_WebIngest(t *testing.T) {
	t.Setenv("WEB_INGEST_ALLOWED_HOSTS", "example.com, semver.org")
	t.Setenv("WEB_INGEST_REFRESH_INTERVAL", "24h")
	t.Setenv("WEB_INGEST_TIMEOUT", "30s")
	t.Setenv("WEB_INGEST_MAX_BYTES", "1048576")
	t.Setenv("WEB_INGEST_MAX_MIRRORS", "50")

	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()

	require.Equal(t, []string{"example.com", "semver.org"}, cfg.WebIngest.AllowedHosts)
	require.Equal(t, "24h0m0s", cfg.WebIngest.RefreshInterval.String())
	require.Equal(t, "30s", cfg.WebIngest.Timeout.String())
	require.Equal(t, 1048576, cfg.WebIngest.MaxBytes)
	require.Equal(t, 50, cfg.WebIngest.MaxMirrors)
}
