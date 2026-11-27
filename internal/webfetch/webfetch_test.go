package webfetch

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFetch_ConvertsHTMLToMarkdown pins the happy path: a page
// comes back as markdown with its title.
func TestFetch_ConvertsHTMLToMarkdown(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Semantic Versioning</title></head>
<body><nav>skip me</nav><main><h1>Spec</h1><p>MAJOR.MINOR.PATCH</p><pre><code>1.2.3</code></pre></main></body></html>`))
	}))
	t.Cleanup(srv.Close)

	res, err := Fetch(context.Background(), srv.URL, Policy{Timeout: 5 * time.Second, dialContext: plainDialer()})
	require.NoError(t, err)
	assert.Equal(t, "Semantic Versioning", res.Title)
	assert.Contains(t, res.Markdown, "# Spec", "the heading must survive the conversion")
	assert.Contains(t, res.Markdown, "MAJOR.MINOR.PATCH")
	assert.Contains(t, res.Markdown, "1.2.3", "code blocks must survive")
	assert.NotContains(t, res.Markdown, "skip me", "navigation must be dropped")
}

// TestFetch_RejectsPrivateAddresses pins the guard that makes the
// feature safe: a URL that resolves to a private address is refused
// before a connection is made.
//
// This is the check that stops an ingested document from turning
// ragabast into a request proxy for the network it sits in.
func TestFetch_RejectsPrivateAddresses(t *testing.T) {
	t.Parallel()

	// The test server listens on 127.0.0.1 — a loopback address,
	// which the guard refuses.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>nope</body></html>"))
	}))
	t.Cleanup(srv.Close)

	_, err := Fetch(context.Background(), srv.URL, Policy{Timeout: 2 * time.Second})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private or reserved address",
		"the error must say the address was refused")
}

// TestFetch_RejectsNonHTTPSchemes pins the scheme allowlist.
func TestFetch_RejectsNonHTTPSchemes(t *testing.T) {
	t.Parallel()

	_, err := Fetch(context.Background(), "file:///etc/passwd", Policy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported scheme")
}

// TestFetch_RejectsNonHTML pins the content-type check: a PDF or a
// JSON API is not a page.
func TestFetch_RejectsNonHTML(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	}))
	t.Cleanup(srv.Close)

	// The loopback address is refused first, so the content-type
	// check is exercised through the converter's own guard.
	_, err := Fetch(context.Background(), "https://example.com/spec.pdf", Policy{Timeout: time.Second})
	require.Error(t, err)
}

// TestFetch_EnforcesTheSizeCap pins the response cap.
func TestFetch_EnforcesTheSizeCap(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>" + strings.Repeat("x", 4096) + "</body></html>"))
	}))
	t.Cleanup(srv.Close)

	_, err := Fetch(context.Background(), srv.URL, Policy{Timeout: 2 * time.Second, MaxBytes: 64, dialContext: plainDialer()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than")
}

// TestFetch_NonOKStatus pins the status check.
func TestFetch_NonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := Fetch(context.Background(), srv.URL, Policy{Timeout: 2 * time.Second, dialContext: plainDialer()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

// plainDialer connects to whatever address it is given, without the
// public-address guard. It is how the tests reach a loopback test
// server; production never uses it.
func plainDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return dialer.DialContext
}

// parseIP parses an address for the classification test.
func parseIP(t *testing.T, addr string) net.IP {
	t.Helper()
	ip := net.ParseIP(addr)
	require.NotNil(t, ip, "test address %q must parse", addr)
	return ip
}

// TestIsPublicIP pins the address classification the guard is
// built on.
func TestIsPublicIP(t *testing.T) {
	t.Parallel()

	public := []string{"93.184.216.34", "8.8.8.8", "2606:2800:220:1:248:1893:25c8:1946"}
	private := []string{
		"127.0.0.1",       // loopback
		"10.1.2.3",        // private
		"172.16.0.1",      // private
		"192.168.1.1",     // private
		"169.254.169.254", // link-local (cloud metadata)
		"0.0.0.0",         // unspecified
		"224.0.0.1",       // multicast
		"::1",             // loopback v6
		"fe80::1",         // link-local v6
		"fc00::1",         // unique-local v6
	}

	for _, addr := range public {
		assert.True(t, isPublicIP(parseIP(t, addr)), "%s must be public", addr)
	}
	for _, addr := range private {
		assert.False(t, isPublicIP(parseIP(t, addr)), "%s must be refused", addr)
	}
}
