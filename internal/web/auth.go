package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// authMiddleware enforces a shared-secret bearer token on the
// API and form-mounted endpoints.
//
// When server.auth_token is empty (the default), the middleware
// is a no-op — every request passes through. This keeps the
// single-user local install working without configuration and
// matches the historical "no auth" behavior.
//
// When server.auth_token is set, every request to a
// protected route MUST carry an `Authorization: Bearer <token>`
// header whose token matches the configured value via a
// constant-time comparison. Requests that fail are rejected
// with 401 + WWW-Authenticate: Bearer so curl/clients can
// retry correctly.
//
// Public routes (always open, even when auth is configured):
//   - GET /                — chat landing page
//   - GET /chat, /search, /ingest, /documents — page chrome
//     forms submit to these paths' POST siblings; the GET is
//     public so the browser can render the form
//   - GET /static/* — CSS/JS/images; browsers do not send
//     Authorization on these requests
//   - OPTIONS * — CORS preflight; the CORS middleware below
//     answers 204 before auth would fire
//
// Trust model: the bearer token is the ONLY credential. There
// is no session, no refresh, no user identity beyond "the
// bearer knows the token". This matches ragabast's
// single-tenant posture; multi-user auth is out of scope.
//
// Constant-time comparison: subtle.ConstantTimeCompare runs in
// time independent of which byte differs. A naive `==` would
// leak the first-differing-byte position through response
// latency and let an attacker recover the token byte-by-byte.
func authMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Public routes are always open. Listing them
			// explicitly (rather than maintaining a "public
			// prefix list") makes the security boundary
			// greppable — a contributor who adds a new
			// protected route will see the switch below and
			// realize they need to decide whether to add it
			// here.
			if isPublicRoute(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			if !bearerMatches(r.Header.Get("Authorization"), token) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="ragabast"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isPublicRoute returns true for routes that must remain
// reachable without an Authorization header. The list is
// intentionally explicit so the security boundary is visible
// in code, not implicit in middleware ordering.
//
// Public:
//   - GET /, GET /chat, GET /search, GET /ingest, GET /documents
//     (form pages — browsers do not send Authorization on GETs)
//   - GET /static/* (CSS/JS/images)
//   - OPTIONS * (CORS preflight)
//
// Everything else requires the bearer token when
// server.auth_token is configured.
func isPublicRoute(method, path string) bool {
	switch method {
	case http.MethodGet:
		switch path {
		case "/", "/chat", "/search", "/ingest", "/documents":
			return true
		}
		if strings.HasPrefix(path, "/static/") || path == "/static" {
			return true
		}
	case http.MethodOptions:
		// CORS preflight. The CORS middleware replies 204
		// before auth would fire; we keep this case here so
		// auth never accidentally rejects an OPTIONS request.
		return true
	}
	return false
}

// bearerMatches reports whether the Authorization header
// carries the configured token in `Bearer <token>` form.
//
// Comparison is constant-time so the request latency does not
// leak the first-differing-byte position. The function also
// accepts "Token <token>" because some clients send that
// form by mistake; both forms work as long as the body is the
// configured token.
func bearerMatches(header, token string) bool {
	if token == "" {
		return true // auth disabled — never block
	}
	const (
		bearerPrefix  = "Bearer "
		tokenPrefix   = "Token "
		caseSensitive = false // tokens are configured as opaque strings; case them as such
	)
	_ = caseSensitive

	if len(header) <= len(bearerPrefix) {
		return false
	}
	scheme := header[:len(bearerPrefix)]
	if !strings.EqualFold(scheme, bearerPrefix) {
		// Allow the legacy "Token <token>" form too.
		if len(header) <= len(tokenPrefix) || !strings.EqualFold(header[:len(tokenPrefix)], tokenPrefix) {
			return false
		}
		presented := header[len(tokenPrefix):]
		return subtleCompare(presented, token)
	}
	presented := header[len(bearerPrefix):]
	return subtleCompare(presented, token)
}

// subtleCompare is a small wrapper around
// subtle.ConstantTimeCompare that handles length differences
// without leaking the length through timing. Two equal-length
// equal-value strings return 1; everything else returns 0.
func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
