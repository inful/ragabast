package web

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/ragabast/internal/reqid"
)

// requestIDHeader is the HTTP header the middleware reads on
// incoming requests and sets on outgoing responses. Matches the
// convention used by AWS, GCP, and other cloud providers so
// operators can reuse existing tracing IDs without translation.
const requestIDHeader = "X-Request-ID"

// maxRequestIDLen caps the accepted client-supplied value. UUIDs
// are 36 chars; W3C trace-context IDs run to 55; 128 leaves
// headroom for vendor prefixes and padding while keeping log
// lines readable and preventing hostile clients from pinning
// megabytes of log output per request.
const maxRequestIDLen = 128

// requestIDMiddleware returns a chi middleware that ensures
// every request carries an X-Request-ID. Reads the header when
// the client supplied a valid value; generates a new UUID
// otherwise. Stores the resolved ID on the request context via
// reqid.WithID and echoes it on the response so the caller can
// see which ID the server assigned.
//
// Install this middleware at the top of the chi chain so every
// subsequent handler — including auth failures and rate-limit
// rejections — runs with a correlation ID on its context.
func requestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimSpace(r.Header.Get(requestIDHeader))
			if !isValidRequestID(id) {
				id = uuid.NewString()
			}
			w.Header().Set(requestIDHeader, id)
			ctx := reqid.WithID(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isValidRequestID reports whether s is acceptable as a client-
// supplied X-Request-ID. Constraints:
//
//   - non-empty after trim (rejects "   ", "\t\n", etc.)
//   - ≤ maxRequestIDLen characters (bounds memory + log readability)
//   - printable ASCII only (rejects newlines, tabs, null bytes,
//     and high-Unicode runes that could split log lines or smuggle
//     HTML into downstream consumers)
//
// The validator trims internally so it is safe to call without
// preprocessing — callers don't have to remember the trim rule.
// The trade-off is that a value like "  abc  " round-trips to
// "abc"; the surrounding whitespace was almost certainly a
// copy-paste artifact rather than meaningful ID content.
func isValidRequestID(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxRequestIDLen {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}
