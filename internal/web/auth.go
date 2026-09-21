package web

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/ragabast/internal/config"
)

// authLabelKey is the unexported context key under which
// authMiddleware stashes the matched token's label (when
// the operator configured one). AccessLogMiddleware and
// any other audit-log code reads it via AuthLabelFromContext.
type authLabelKey struct{}

// AuthLabelFromContext returns the auth_label attributed to
// the bearer credential that authenticated the request, or
// "" when no label was configured for the matching token
// (or when auth is disabled and no comparison ran).
//
// The label is operational attribution only — it does not
// grant any privilege. Two tokens may share a label.
func AuthLabelFromContext(ctx context.Context) string {
	v, _ := ctx.Value(authLabelKey{}).(string)
	return v
}

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
// authMiddleware enforces a bearer-token check on the API
// and form-mounted endpoints. tokens is the merged list of
// effective tokens (see ServerConfig.EffectiveAuthTokens):
// the singular AuthToken plus the AuthTokens list, deduped.
//
// When tokens is empty (the default), the middleware is a
// no-op — every request passes through. This keeps the
// single-user local install working without configuration
// and matches the historical "no auth" behavior.
//
// When tokens is non-empty, every request to a protected
// route MUST carry `Authorization: Bearer <token>` matching
// one of the configured values via constant-time comparison.
// Requests that fail are rejected with 401 + WWW-Authenticate:
// Bearer so curl/clients can retry correctly.
//
// If the matched token has a label configured, the label is
// stashed on the request context (AuthLabelFromContext) so
// the access log can attribute traffic to a specific
// consumer. Labels are operational attribution only — they
// do not grant any privilege.
func authMiddleware(tokens []config.AuthToken) func(http.Handler) http.Handler {
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

			label, ok := matchBearer(r.Header.Get("Authorization"), tokens)
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="ragabast"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			if label != "" {
				ctx := context.WithValue(r.Context(), authLabelKey{}, label)
				r = r.WithContext(ctx)
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

// matchBearer reports whether the Authorization header carries
// any of the configured tokens in `Bearer <token>` (or the
// legacy `Token <token>`) form, returning the matched
// token's label and true on success.
//
// Comparison is constant-time per token so the request
// latency does not leak the first-differing-byte position.
// Each configured token is compared in turn; the loop is
// short (typically 1-3 entries, capping at the operator's
// fleet size) so timing-based attacks against the number of
// configured tokens are out of scope.
func matchBearer(header string, tokens []config.AuthToken) (label string, ok bool) {
	if len(tokens) == 0 {
		return "", true // auth disabled — never block
	}
	const (
		bearerPrefix = "Bearer "
		tokenPrefix  = "Token "
	)
	if len(header) <= len(bearerPrefix) {
		return "", false
	}
	scheme := header[:len(bearerPrefix)]
	var presented string
	if strings.EqualFold(scheme, bearerPrefix) {
		presented = header[len(bearerPrefix):]
	} else if len(header) > len(tokenPrefix) && strings.EqualFold(header[:len(tokenPrefix)], tokenPrefix) {
		// Legacy "Token <token>" form.
		presented = header[len(tokenPrefix):]
	} else {
		return "", false
	}
	for _, t := range tokens {
		if subtleCompare(presented, t.Value) {
			return t.Label, true
		}
	}
	return "", false
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
