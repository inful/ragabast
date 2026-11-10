package web

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
)

// redactedQueryKeys is the set of URL query keys whose values
// the access logger MUST replace with [REDACTED] before the
// line is written. The set is intentionally small and explicit
// so a future contributor who adds a new sensitive field can
// grep for it and decide whether to add the key.
//
// Today:
//   - query:      the natural-language query on /search and
//     /api/search. May contain operator content
//     the operator did not intend to log.
//   - message:    the chat message on POST /chat/message.
//     Same rationale as query.
//   - text:       the body of POST /api/link-suggestions and
//     POST /api/frontmatter/suggest — same
//     rationale.
//   - content:    the docbuilder markdown body posted to
//     /api/ingest, /api/ingest/raw,
//     /api/ingest/file. May be many KiB.
//   - document_id, docbuilder_base_url: path-shaped fields
//     that may carry sensitive identifiers.
//   - x-algolia-api-key: the Algolia DocSearch API key, when
//     the client is configured with authMode=withinQueryParameters
//     and sends it in the URL.
//
// The bearer token (Authorization header) is NEVER logged —
// the logChatRequests path in internal/vector/openai.go writes
// only the request/response bodies, not headers.
var redactedQueryKeys = map[string]struct{}{
	"query":               {},
	"message":             {},
	"text":                {},
	"content":             {},
	"document_id":         {},
	"docbuilder_base_url": {},
	"x-algolia-api-key":   {},
}

// redactQueryString returns a copy of rawQuery with every
// value belonging to redactedQueryKeys replaced by
// "[REDACTED]". Keys not in the list are passed through
// unchanged so the access log still shows path-relevant
// information (tag=foo, category=Reference, etc.).
//
// The function is allocation-friendly: it walks the parsed
// query once and writes a new RawQuery using url.Values.Encode.
// Order is not preserved (Encode sorts alphabetically); that
// is acceptable for log output and avoids the cost of
// preserving insertion order.
func redactQueryString(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		// Parse failed: do not try to redact a malformed
		// string (we'd risk leaking whatever the user
		// sent). Return empty and let the operator
		// investigate the malformed request.
		return ""
	}
	for k := range values {
		if _, ok := redactedQueryKeys[k]; ok {
			values[k] = []string{"[REDACTED]"}
		}
	}
	return values.Encode()
}

// redactingResponseWriter wraps http.ResponseWriter so the
// access logger sees a request with redacted query values.
// chi's middleware.Logger inspects r.URL.RawQuery at the
// time the request finishes, not when it arrives — so we
// need to mutate the URL in place, not copy.
// redactedQueryKey is the context key under which the redacted query
// string is stashed for the access logger.
type redactedQueryKey struct{}

// redactAccessLogMiddleware computes the redacted query string and
// stashes it on the request context.
//
// The live request is NOT modified: the handler must still see the
// real query string. The access logger reads the redacted copy from
// the context (see redactedLogFormatter).
//
// Why the copy matters: chi's DefaultLogFormatter logs
// r.RequestURI, not r.URL.RawQuery. The previous implementation
// rewrote r.URL.RawQuery in place, which never reached the log line
// at all — the operator's query text was logged verbatim — while
// still replacing the query the handler was about to read.
func redactAccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redacted := ""
		if r.URL != nil {
			redacted = redactQueryString(r.URL.RawQuery)
		}
		ctx := context.WithValue(r.Context(), redactedQueryKey{}, redacted)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// redactedLogFormatter is chi's default log formatter with one
// change: the request it logs carries the redacted query string.
//
// The formatter builds its entry from a COPY of the request whose
// RequestURI is redacted, so the handler keeps the original URL.
type redactedLogFormatter struct {
	middleware.DefaultLogFormatter
}

// NewLogEntry returns a log entry for the redacted copy of r.
func (f redactedLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	redacted, ok := r.Context().Value(redactedQueryKey{}).(string)
	if !ok {
		return f.DefaultLogFormatter.NewLogEntry(r)
	}
	clone := r.Clone(r.Context())
	clone.RequestURI = redactedRequestURI(r, redacted)
	return f.DefaultLogFormatter.NewLogEntry(clone)
}

// newRedactedLogFormatter builds the access-log formatter. It mirrors
// chi's own DefaultLogger setup (stdout, standard flags, color
// everywhere but Windows).
func newRedactedLogFormatter() redactedLogFormatter {
	return redactedLogFormatter{
		DefaultLogFormatter: middleware.DefaultLogFormatter{
			Logger:  log.New(os.Stdout, "", log.LstdFlags),
			NoColor: runtime.GOOS == "windows",
		},
	}
}

// redactedRequestURI rebuilds the request URI with the redacted
// query string. The path is copied verbatim; a redacted query that
// came back empty (a malformed query, or no query at all) drops the
// query entirely.
func redactedRequestURI(r *http.Request, redactedQuery string) string {
	uri := r.RequestURI
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri = uri[:i]
	}
	if redactedQuery == "" {
		return uri
	}
	return uri + "?" + redactedQuery
}
