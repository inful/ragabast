package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSafeRealIP_DirectConnectionIgnoresXFF pins the spoofing
// fix in audit finding 8: a request that arrives directly
// (r.RemoteAddr is the client's IP, not a loopback proxy) MUST
// NOT have r.RemoteAddr rewritten from XFF. The pre-fix chi
// middleware.RealIP did, which let a hostile client forge any
// XFF and poison the per-IP rate limiter.
func TestSafeRealIP_DirectConnectionIgnoresXFF(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.42:55555" // RFC 5737 doc-range; not loopback
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Real-IP", "127.0.0.1")

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "203.0.113.42:55555", r.RemoteAddr,
			"safeRealIP must ignore XFF when r.RemoteAddr is not loopback")
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}

// TestSafeRealIP_LoopbackConnectionTrustsXFF pins the
// backward-compatible case: a request whose TCP peer is
// loopback (the typical reverse-proxy-on-localhost deployment)
// DOES honor XFF, because the operator's nginx / caddy /
// traefik is trusted when the connection comes from the
// same machine.
func TestSafeRealIP_LoopbackConnectionTrustsXFF(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "203.0.113.42:0", r.RemoteAddr,
			"safeRealIP must take the leftmost XFF entry when r.RemoteAddr is loopback")
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}

// TestSafeRealIP_FallsBackToXRealIP pins the X-Real-IP fallback:
// some proxies (nginx default, Caddy) send X-Real-IP alone
// without XFF. We honor it the same way.
func TestSafeRealIP_FallsBackToXRealIP(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:54321"
	req.Header.Set("X-Real-IP", "203.0.113.99")

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "203.0.113.99:0", r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}

// TestSafeRealIP_NoHeadersLeavesRemoteAddrAlone pins the
// no-headers case: a loopback request with no XFF / X-Real-IP
// (e.g. an operator poking localhost directly) keeps its
// r.RemoteAddr unchanged.
func TestSafeRealIP_NoHeadersLeavesRemoteAddrAlone(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "127.0.0.1:54321", r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}

// TestSafeRealIP_PicksRightmostXFFEntry pins the multi-hop
// XFF behavior: XFF is appended-to by each proxy in the chain,
// so the rightmost entry is the address the nearest proxy
// observed on the TCP connection. That is the only entry the
// client cannot forge, and it is what the rate limiter keys
// on.
//
// This test previously asserted the leftmost entry. That was
// the rate-limit bypass in audit finding 2: the leftmost entry
// is whatever the client put in its own inbound XFF header,
// which nginx's default $proxy_add_x_forwarded_for copies
// verbatim into the forwarded header.
func TestSafeRealIP_PicksRightmostXFFEntry(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.1, 10.0.0.5, 10.0.0.6")

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "10.0.0.6:0", r.RemoteAddr,
			"safeRealIP must take the rightmost (proxy-observed) XFF entry")
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}
