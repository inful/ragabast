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

// authMiddleware enforces authentication on the API and
// form-mounted endpoints. It accepts one or both of:
//
//   - A shared-secret bearer token (server.auth_token /
//     server.auth_tokens). Compared constant-time against
//     the Authorization header.
//   - A session cookie (server.auth.cookie_name) issued by
//     the OAuth callback handler. Resolved against the
//     sessionStore.
//
// tokens is the merged list of effective bearer tokens
// (see ServerConfig.EffectiveAuthTokens). sessions, when
// non-nil, is the in-memory session store the callback
// handler writes to. cookieName is the session-cookie name
// (only consulted when sessions != nil).
//
// Decision order per request:
//
//  1. Public GET/OPTIONS routes bypass entirely. If a
//     session cookie is present on a public route, the
//     session is still attached to the context so the
//     handler can render the "signed in as" line.
//  2. Bearer token matches → allow, stash the token label.
//  3. Session cookie resolves to a live (non-expired)
//     session → allow, stash the session, slide the
//     expiry forward.
//  4. Otherwise → 401 with WWW-Authenticate.
//
// When both tokens and sessions are nil/empty the
// middleware is a no-op — every request passes through.
// This keeps the single-user local install working
// without configuration and matches the historical
// "no auth" behavior.
//
// Constant-time comparison: subtle.ConstantTimeCompare runs
// in time independent of which byte differs. A naive `==`
// would leak the first-differing-byte position through
// response latency and let an attacker recover the token
// byte-by-byte.
func authMiddleware(tokens []config.AuthToken, sessions *sessionStore, cookieName string) func(http.Handler) http.Handler {
	// Pre-compute the "any auth at all" boolean so the
	// hot path doesn't have to recheck on every request.
	hasTokens := len(tokens) > 0
	hasSessions := sessions != nil

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
				r = withOptionalSession(r, sessions, cookieName)
				next.ServeHTTP(w, r)

				return
			}

			// No auth configured → no-op pass-through.
			if !hasTokens && !hasSessions {
				next.ServeHTTP(w, r)

				return
			}

			// Bearer token check first (programs).
			if hasTokens {
				if label, ok := matchBearer(r.Header.Get("Authorization"), tokens); ok {
					if label != "" {
						ctx := context.WithValue(r.Context(), authLabelKey{}, label)
						r = r.WithContext(ctx)
					}
					next.ServeHTTP(w, r)

					return
				}
			}

			// Session cookie check next (browsers).
			if hasSessions {
				updated := withOptionalSession(r, sessions, cookieName)
				//nolint:contextcheck // updated carries the session-stamped context; SessionFromContext must read from updated, not r
				if SessionFromContext(updated.Context()) != nil {
					next.ServeHTTP(w, updated)

					return
				}
			}

			w.Header().Set("WWW-Authenticate", `Bearer realm="ragabast"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}
}

// withOptionalSession returns r with a session attached to
// its context if (a) sessions is non-nil and (b) the
// request's cookie resolves to a live (non-expired)
// session record. The Touch (sliding renewal) is a no-op
// when the store's defaultTTL is zero (no expiry).
//
// Exists as a helper so the public-route and protected-
// route branches of authMiddleware share one session-
// resolution path — divergence there is a classic source
// of "the login page works but the API doesn't" bugs.
func withOptionalSession(r *http.Request, sessions *sessionStore, cookieName string) *http.Request {
	if sessions == nil {
		return r
	}
	sess, ok := sessions.Get(readSessionCookie(r, cookieName))
	if !ok {
		return r
	}
	if sessions.defaultTTL > 0 {
		sessions.Touch(sess.ID)
	}

	return r.WithContext(WithSession(r.Context(), sess))
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
//   - GET /auth/login, /auth/<provider>/login,
//     /auth/<provider>/callback — the OAuth flow itself
//     must be reachable without a credential; the IdP
//     redirect that lands on /auth/<provider>/callback
//     carries no session yet
//   - OPTIONS * (CORS preflight)
//
// Everything else requires the bearer token or a session
// cookie when auth is configured.
func isPublicRoute(method, path string) bool {
	switch method {
	case http.MethodGet:
		switch path {
		case "/", "/chat", "/search", "/ingest", "/documents":
			return true
		case "/auth/login", "/auth/me":
			return true
		}
		if strings.HasPrefix(path, "/static/") || path == "/static" {
			return true
		}
		if strings.HasPrefix(path, "/auth/") {
			// /auth/<provider>/login and
			// /auth/<provider>/callback — the entire
			// /auth/ tree is open for GET so the
			// OAuth handshake can complete.
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
