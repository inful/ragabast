package web

import (
	"net/http"
	"strings"
)

// corsMiddleware applies the operator-configured CORS policy.
//
// History: the previous middleware hard-coded
// `Access-Control-Allow-Origin: *` whenever EnableCORS was
// true. Combined with allowing the Authorization header, that
// meant any browser visiting any third-party site could issue
// authenticated cross-origin requests against ragabast. This
// is a textbook CSRF setup (the C-2 finding).
//
// New behavior: the middleware echoes the request Origin
// header only when it appears in the configured allow-list
// (cors_origins). An empty allow-list disables CORS entirely;
// the explicit literal "*" in the allow-list preserves the
// old behavior for trusted local-only deployments but is not
// the default.
//
// The middleware also answers OPTIONS preflight requests with
// 204 + the appropriate Access-Control-Allow-* headers. It
// runs BEFORE authMiddleware in the chain so preflight
// requests do not require a bearer token — that matches the
// browser behavior (preflight is sent without credentials).
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	// Build a set for O(1) lookup. An empty set means "no
	// cross-origin allowed" — the safe default.
	allow := make(map[string]struct{}, len(allowedOrigins))
	wildcard := false
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			wildcard = true
			continue
		}
		allow[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Same-origin or no Origin header: no CORS
			// response headers needed.
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Allowed origin? Echo it back. We MUST echo the
			// exact origin (not "*") when credentials are
			// allowed; the Access-Control-Allow-Credentials
			// path is not enabled here because Authorization
			// is a bearer header (not a cookie), but the
			// echo-exact-origin discipline matches what a
			// future cookie-based auth would require.
			if wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if _, ok := allow[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			} else {
				// Disallowed origin. Don't echo any ACAO
				// header — the browser will refuse the
				// response. Fall through to next so the
				// request still serves (the JSON body is
				// readable from same-origin pages); the
				// browser-side CORS check is what blocks
				// the cross-origin reader.
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			// Preflight: respond 204 without invoking the
			// route handler. Browsers do not send credentials
			// on preflight, so this MUST run before auth.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
