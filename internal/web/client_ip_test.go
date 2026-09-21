package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClientIPMiddleware_ExtractsIPv4 covers the common case:
// chi has set r.RemoteAddr to the client's address after the
// RealIP middleware ran, and our middleware pulls the host
// portion out of the "host:port" form.
func TestClientIPMiddleware_ExtractsIPv4(t *testing.T) {
	var ctxIP string
	h := clientIPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxIP = ClientIPFromContext(r.Context())
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest/async", nil)
	req.RemoteAddr = "10.0.0.42:54321"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, "10.0.0.42", ctxIP,
		"middleware must strip the port and yield just the host")
}

// TestClientIPMiddleware_ExtractsIPv6 verifies IPv6 round-trip:
// r.RemoteAddr comes in as "[::1]:8080" (with brackets) and
// the middleware must yield "::1" (without brackets or port).
func TestClientIPMiddleware_ExtractsIPv6(t *testing.T) {
	var ctxIP string
	h := clientIPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxIP = ClientIPFromContext(r.Context())
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	req.RemoteAddr = "[::1]:8080"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, "::1", ctxIP,
		"IPv6 brackets and port suffix must be stripped")
}

// TestClientIPMiddleware_HonorsXForwardedFor exercises the
// integration with chi/middleware.RealIP: when the chain has
// both middlewares, an X-Forwarded-For header causes the
// client's IP to be promoted to r.RemoteAddr before our
// middleware extracts it. Verified by running both
// middlewares in sequence rather than mocking RealIP — the
// chi behavior is what matters at this layer.
func TestClientIPMiddleware_HonorsXForwardedFor(t *testing.T) {
	var ctxIP string
	h := chimw.RealIP(clientIPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxIP = ClientIPFromContext(r.Context())
	})))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.42")
	req.RemoteAddr = "127.0.0.1:54321" // what the proxy connection looks like
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, "203.0.113.42", ctxIP,
		"X-Forwarded-For must be honored via chi/middleware.RealIP")
}

// TestClientIPMiddleware_HandlesNoPort covers the rare case
// where r.RemoteAddr has no port (some unix-socket setups,
// test fixtures). The middleware should still record whatever
// it sees rather than dropping the request.
func TestClientIPMiddleware_HandlesNoPort(t *testing.T) {
	var ctxIP string
	h := clientIPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxIP = ClientIPFromContext(r.Context())
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.99"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, "10.0.0.99", ctxIP,
		"RemoteAddr without a port is recorded verbatim")
}

// TestClientIPMiddleware_EmptyRemoteAddr documents that an
// empty RemoteAddr (rare; only seen in some test fixtures)
// is recorded as empty on the context. ClientIPFromContext
// returns "unknown" in that case — the audit log fallback
// for the truly-missing case.
func TestClientIPMiddleware_EmptyRemoteAddr(t *testing.T) {
	var ctxIP string
	h := clientIPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxIP = ClientIPFromContext(r.Context())
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	req.RemoteAddr = ""
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Empty(t, ctxIP,
		"empty RemoteAddr is recorded as empty on the context")
}

// TestClientIPFromContext_UnknownWhenAbsent pins the no-
// middleware contract: callers that read the IP without the
// middleware in the chain (CLI commands, workers running
// outside the HTTP server) get "unknown" rather than a
// panic or an empty value. This preserves backward
// compatibility with the original clientIPFromContext
// stub while opening the door to a real value when the
// HTTP chain ran.
func TestClientIPFromContext_UnknownWhenAbsent(t *testing.T) {
	assert.Equal(t, "unknown", ClientIPFromContext(context.Background()))
}
