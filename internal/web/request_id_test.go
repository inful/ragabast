package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/reqid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequestIDMiddleware_GeneratesUUIDWhenAbsent verifies the
// happy path for clients that do not send their own correlation
// ID: the middleware mints one, stores it on the request context,
// and surfaces it as a response header.
func TestRequestIDMiddleware_GeneratesUUIDWhenAbsent(t *testing.T) {
	var ctxID string
	h := requestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = reqid.FromContext(r.Context())
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	got := rr.Header().Get(requestIDHeader)
	assert.NotEmpty(t, got, "response must carry X-Request-ID")
	assert.Equal(t, got, ctxID, "context ID matches response header")
}

// TestRequestIDMiddleware_EchoesClientSuppliedID covers the case
// where an upstream proxy / load balancer / caller already
// assigned an ID. The middleware must echo it verbatim so a
// distributed trace stays whole end-to-end.
func TestRequestIDMiddleware_EchoesClientSuppliedID(t *testing.T) {
	const supplied = "client-supplied-correlation-id-abc-123"

	h := requestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, supplied, reqid.FromContext(r.Context()),
			"context value matches the echoed header")
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(requestIDHeader, supplied)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, supplied, rr.Header().Get(requestIDHeader),
		"client-supplied IDs are echoed unchanged")
}

// TestRequestIDMiddleware_RejectsEmptyHeader documents that a
// blank header (common from misconfigured clients) is treated the
// same as absent — the middleware generates a fresh ID rather
// than letting the empty value leak into downstream logs and
// async-job records.
func TestRequestIDMiddleware_RejectsEmptyHeader(t *testing.T) {
	h := requestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(requestIDHeader, "   ")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	got := rr.Header().Get(requestIDHeader)
	assert.NotEqual(t, "   ", got, "whitespace-only IDs are rejected")
	assert.NotEmpty(t, got, "rejected IDs are replaced with a generated one")
}

// TestRequestIDMiddleware_RejectsTooLong caps the size of a
// client-supplied ID. Without this guard, a hostile client could
// pin megabytes of log output per request. 128 chars covers
// standard UUIDs (36) and W3C trace-context IDs (55) with
// headroom for vendor-specific prefixes.
func TestRequestIDMiddleware_RejectsTooLong(t *testing.T) {
	h := requestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	long := strings.Repeat("a", 200)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(requestIDHeader, long)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.NotEqual(t, long, rr.Header().Get(requestIDHeader),
		"oversized IDs are rejected and replaced")
}

// TestRequestIDMiddleware_RejectsControlChars prevents log
// injection: a value containing newlines would split the audit
// line and let an attacker forge ingest-job events. The
// validator requires printable ASCII only.
func TestRequestIDMiddleware_RejectsControlChars(t *testing.T) {
	h := requestIDMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set(requestIDHeader, "id\nINJECTED LOG LINE")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	got := rr.Header().Get(requestIDHeader)
	assert.NotContains(t, got, "\n", "newline would split log lines")
	assert.NotEqual(t, "id\nINJECTED LOG LINE", got,
		"control chars are rejected to prevent log injection")
}

// TestRequestIDFromContext_EmptyWhenAbsent documents the no-
// middleware contract: callers that pull the ID without the
// middleware in the chain get "" rather than a panic or a
// fabricated value. Useful for code paths that run both inside
// and outside the HTTP server (CLI commands, async workers).
func TestRequestIDFromContext_EmptyWhenAbsent(t *testing.T) {
	assert.Empty(t, reqid.FromContext(context.Background()))
}

// TestIsValidRequestID covers the validator's edge cases. The
// rules are: non-empty after trim, ≤ maxRequestIDLen chars,
// printable ASCII only (no newlines, tabs, control chars, or
// non-ASCII runes).
func TestIsValidRequestID(t *testing.T) {
	long := strings.Repeat("a", 129)
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"whitespace only", "   ", false},
		{"uuid", "550e8400-e29b-41d4-a716-446655440000", true},
		{"trace id", "abc-def-123", true},
		{"trace context", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", true},
		{"at limit", strings.Repeat("a", 128), true},
		{"too long", long, false},
		{"with newline", "id\nINJECT", false},
		{"with tab", "id\tINJECT", false},
		{"with high unicode", "id\u00e9", false},
		{"with null", "id\x00", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isValidRequestID(tc.in))
		})
	}
}
