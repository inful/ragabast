package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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

// TestRedactAccessLogMiddleware_RedactsTheLoggedURI is an
// integration test that asserts the access log carries the redacted
// query string while the handler still sees the real one.
//
// chi's default formatter logs r.RequestURI, NOT r.URL.RawQuery.
// The previous implementation rewrote r.URL.RawQuery in place,
// which never reached the log line at all: the operator's query text
// was logged verbatim. The old test drove a custom formatter that
// read r.URL.RawQuery, so it passed while the real logger leaked.
//
// This test drives chi's REAL formatter and asserts on what it
// writes.
func TestRedactAccessLogMiddleware_RedactsTheLoggedURI(t *testing.T) {
	var logged bytes.Buffer
	formatter := redactedLogFormatter{
		DefaultLogFormatter: chimw.DefaultLogFormatter{
			Logger:  &captureLogger{buf: &logged},
			NoColor: true,
		},
	}

	var handlerQuery string
	r := chi.NewRouter()
	r.Use(redactAccessLogMiddleware)
	r.Use(chimw.RequestLogger(formatter))
	r.Get("/search", func(_ http.ResponseWriter, req *http.Request) {
		handlerQuery = req.URL.Query().Get("query")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/search?query=SECRET&tag=foo", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, "SECRET", handlerQuery,
		"the handler must see the real query string")
	require.NotContains(t, logged.String(), "SECRET",
		"the secret query value must not appear in the access log")
	require.Contains(t, logged.String(), "query=%5BREDACTED%5D",
		"the access log must show the redacted query value")
	require.Contains(t, logged.String(), "tag=foo",
		"non-sensitive query keys must pass through unchanged")
}

// captureLogger is a chi LoggerInterface that writes into a buffer
// so the test can assert on the access-log line.
type captureLogger struct {
	buf *bytes.Buffer
}

func (l *captureLogger) Print(v ...any) {
	for _, item := range v {
		fmt.Fprint(l.buf, item)
	}
}

// _ keeps huma import live so this test file compiles
// alongside the existing test suite.
var _ = huma.Error400BadRequest
