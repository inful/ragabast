package web

import (
	"net/http"
	"net/url"
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
//     /api/ingest, /ingest, /api/ingest/raw,
//     /api/ingest/file. May be many KiB.
//   - document_id, docbuilder_base_url: path-shaped fields
//     that may carry sensitive identifiers.
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
func redactRequestURL(r *http.Request) {
	if r.URL == nil {
		return
	}
	rawQuery := r.URL.RawQuery
	if rawQuery == "" {
		return
	}
	redacted := redactQueryString(rawQuery)
	if redacted == "" {
		// Parse failed: drop the query entirely.
		r.URL.RawQuery = ""
		return
	}
	r.URL.RawQuery = redacted
}

// redactAccessLogMiddleware wraps chi's middleware.Logger so
// every request URL is redacted before the access line is
// written. The middleware sits in front of middleware.Logger
// and re-writes r.URL.RawQuery in place; chi then logs the
// rewritten URL.
//
// Why in-place mutation instead of a wrapper Writer: chi's
// middleware.Logger reads r.URL.RawQuery at log time and
// formats the line itself. Mutating the URL is the only way
// to influence what chi writes.
func redactAccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redactRequestURL(r)
		next.ServeHTTP(w, r)
	})
}
