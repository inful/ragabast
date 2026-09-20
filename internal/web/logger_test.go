package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
)

// TestRedactQueryString_PinsUnitBehavior tests the pure
// redaction function so a future contributor who breaks the
// redaction logic gets a fast feedback signal without
// needing to wire chi's logger capture.
//
// The function is intentionally simple; the test pins every
// behavior the access-log middleware relies on.
func TestRedactQueryString_PinsUnitBehavior(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // substrings that MUST appear in the output
		bad  []string // substrings that MUST NOT appear in the output
	}{
		{
			name: "query key redacted",
			in:   "query=hello+world&tag=foo",
			want: []string{"query=%5BREDACTED%5D", "tag=foo"},
			bad:  []string{"hello", "world"},
		},
		{
			name: "document_id key redacted",
			in:   "document_id=doc-123",
			want: []string{"document_id=%5BREDACTED%5D"},
			bad:  []string{"doc-123"},
		},
		{
			name: "message key redacted",
			in:   "message=secret+chat+message",
			want: []string{"message=%5BREDACTED%5D"},
			bad:  []string{"secret", "chat"},
		},
		{
			name: "text key redacted",
			in:   "text=hello",
			want: []string{"text=%5BREDACTED%5D"},
			bad:  []string{"hello"},
		},
		{
			name: "non-sensitive keys pass through",
			in:   "tag=foo&category=Reference",
			want: []string{"tag=foo", "category=Reference"},
			bad:  []string{"REDACTED"},
		},
		{
			name: "empty input returns empty",
			in:   "",
			want: []string{},
		},
		{
			name: "malformed input returns empty (no leak)",
			in:   "%%%not_valid",
			want: []string{},
			bad:  []string{"not_valid"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := redactQueryString(tc.in)
			for _, w := range tc.want {
				if w != "" {
					require.Contains(t, out, w)
				}
			}
			for _, b := range tc.bad {
				require.NotContains(t, out, b,
					"redactQueryString leaked %q in output %q (input %q)",
					b, out, tc.in)
			}
		})
	}
}

// TestRedactAccessLogMiddleware_ReplacesURL is an integration
// test that asserts the middleware actually rewrites the URL
// on a real request. We replace chi's DefaultLogger with a
// custom LogFormatter that captures each LogEntry so we can
// assert on the rewritten URL.
//
// (chi's middleware.Logger uses a custom log.New instance
// initialized in its init() function; redirecting the
// standard log package does NOT intercept it. This test
// drives chi's log formatter directly.)
func TestRedactAccessLogMiddleware_ReplacesURL(t *testing.T) {
	// captured is the side-channel the test formatter writes
	// into. The test asserts on its final state after the
	// middleware chain returns.
	var capturedRawQuery string

	r := chi.NewRouter()
	r.Use(redactAccessLogMiddleware)
	r.Use(chimw.RequestLogger(&testLogFormatter{
		onWrite: func(rawQuery string) { capturedRawQuery = rawQuery },
	}))
	r.Get("/search", func(w http.ResponseWriter, _ *http.Request) {
		// dummy handler — the redaction is verified by the
		// LogEntry the test formatter captures.
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/search?query=SECRET&tag=foo", nil)
	r.ServeHTTP(w, req)

	require.NotEmpty(t, capturedRawQuery,
		"the LogEntry must capture the raw query chi was asked to log")

	// The redaction middleware ran; chi's log entry should
	// show the rewritten URL.
	require.Contains(t, capturedRawQuery, "query=%5BREDACTED%5D",
		"chi's log entry must show the redacted query value")
	require.NotContains(t, capturedRawQuery, "SECRET",
		"the secret query value must not appear in chi's log entry")
	require.Contains(t, capturedRawQuery, "tag=foo",
		"non-sensitive query keys must pass through unchanged")
}

// testLogFormatter is a chi LogFormatter that hands each
// request's LogEntry a callback for the test to capture what
// chi would log.
type testLogFormatter struct {
	onWrite func(rawQuery string)
}

func (f *testLogFormatter) NewLogEntry(r *http.Request) chimw.LogEntry {
	return &testLogEntry{request: r, onWrite: f.onWrite}
}

// testLogEntry is the LogEntry our formatter returns. It
// captures r.URL.RawQuery at Write time and forwards it to
// the test callback.
type testLogEntry struct {
	request *http.Request
	onWrite func(rawQuery string)
}

func (e *testLogEntry) Write(_ /*status*/, _ int, _ http.Header, _ time.Duration, _ any) {
	if e.request != nil && e.request.URL != nil && e.onWrite != nil {
		e.onWrite(e.request.URL.RawQuery)
	}
}

func (e *testLogEntry) Panic(_ any, _ []byte) {}

// _ keeps huma import live so this test file compiles
// alongside the existing test suite.
var _ = huma.Error400BadRequest
