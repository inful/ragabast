package web

import (
	"net/http"
)

// maxRequestBodyBytes caps the body size on every request the
// web layer handles. The Huma API endpoints have their own
// per-endpoint cap (configurable via huma.Config.MaxBodyBytes,
// default 10 MiB) so they are not affected by this limit; the
// form-based endpoints (POST /chat/message, POST /search,
// POST /ingest) flow through Go's net/http directly and had
// no upper bound before this middleware.
//
// The cap matches Huma's default so the two code paths share a
// single mental model for "how big is too big". Operators who
// need a different limit can override the package-level
// variable in their integration tests; production callers
// should not change it.
const maxRequestBodyBytes int64 = 10 << 20 // 10 MiB

// maxBytesReaderMiddleware wraps every request's Body with
// http.MaxBytesReader so handlers that read the body directly
// (the chi-mounted form endpoints: POST /chat/message,
// POST /search, POST /ingest) get a 413 when a client exceeds
// maxRequestBodyBytes. Without this, a single attacker can
// POST a multi-GB document and OOM the server.
//
// Huma-mounted endpoints already enforce their own body-size
// cap (huma.Config.MaxBodyBytes); this middleware is a no-op
// for them because they never call r.Body.Read.
func maxBytesReaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
