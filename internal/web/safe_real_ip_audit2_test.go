package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSafeRealIP_TakesRightmostXFFEntry pins audit finding 2.
//
// X-Forwarded-For is an append-to header: each proxy in the
// chain adds the address of the peer it received from, on the
// RIGHT. So the header reads "client, proxy1, proxy2" and the
// rightmost entry is the one the LAST proxy wrote.
//
// The old code took the LEFTMOST entry — the original client,
// as reported by the proxy. That value is the one the proxy
// COPIED from the inbound request, which means a client that
// sends its own X-Forwarded-For header can choose it.
//
// Concretely, with nginx's default
// `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for`
// a client sending "X-Forwarded-For: 1.2.3.4" gets
// "1.2.3.4, <nginx's view of the client>" forwarded. Taking the
// leftmost honors the attacker's value.
//
// The rightmost entry is the address the nearest proxy actually
// observed on the TCP connection — the one value in the header
// the client cannot forge.
func TestSafeRealIP_TakesRightmostXFFEntry(t *testing.T) {
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

// TestSafeRealIP_SingleEntryXFFIsUnchanged covers the common
// single-proxy case: one entry in the header is both the
// leftmost and the rightmost, so it is taken as-is.
func TestSafeRealIP_SingleEntryXFFIsUnchanged(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "203.0.113.42:0", r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}

// TestSafeRealIP_IgnoresNonIPXFFEntry pins the validation
// half of finding 2. A proxy may forward a header value that
// is not an IP at all ("unknown", a hostname, a port). It must
// not be written into r.RemoteAddr: the value ends up as a
// rate-limiter bucket key and an audit field, and a
// non-IP string lets a client mint unlimited buckets by
// varying the garbage.
func TestSafeRealIP_IgnoresNonIPXFFEntry(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.1, unknown")

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "127.0.0.1:54321", r.RemoteAddr,
			"a non-IP XFF entry must be ignored, not written to RemoteAddr")
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}

// TestSafeRealIP_IgnoresNonIPXRealIP is the same contract for
// the X-Real-IP fallback header.
func TestSafeRealIP_IgnoresNonIPXRealIP(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Real-IP", "not-an-ip")

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "127.0.0.1:54321", r.RemoteAddr,
			"a non-IP X-Real-IP must be ignored, not written to RemoteAddr")
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}

// TestSafeRealIP_AcceptsIPv6XFFEntry pins that validation does
// not reject legitimate IPv6 clients. The value is bracketed
// when written back so net.SplitHostPort can still split it.
func TestSafeRealIP_AcceptsIPv6XFFEntry(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "2001:db8::1")

	handler := safeRealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		assert.NoError(t, err, "RemoteAddr must remain host:port splittable")
		assert.Equal(t, "2001:db8::1", host)
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
}
