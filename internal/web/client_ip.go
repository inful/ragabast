package web

import (
	"context"
	"net"
	"net/http"
)

// clientIPContextKey is the typed key for storing the parsed
// client IP on context.Context. As an unexported struct type,
// it cannot collide with any string-typed key an external
// caller might use, so context.Value returns "" rather than
// a stale value when the middleware did not run.
type clientIPContextKey struct{}

// ClientIPFromContext returns the client IP stored on ctx by
// clientIPMiddleware, or "unknown" when the middleware did not
// run (e.g., a non-HTTP caller like a CLI command or a worker
// goroutine). Returns the host portion of r.RemoteAddr — IPv6
// brackets and the port suffix are stripped via net.SplitHostPort.
//
// The "unknown" fallback preserves the original audit-log
// contract: if a job was somehow submitted without going through
// the HTTP chain (e.g., an internal test helper), the audit
// trail still has a recognizable placeholder rather than an
// empty value that would silently match zero-length IP
// prefixes.
func ClientIPFromContext(ctx context.Context) string {
	v, _ := ctx.Value(clientIPContextKey{}).(string)
	if v == "" {
		return "unknown"
	}
	return v
}

// clientIPMiddleware extracts the client IP from r.RemoteAddr
// (already adjusted by chi/middleware.RealIP for X-Forwarded-
// For / X-Real-IP) and stores it on the request context.
//
// Install this middleware AFTER chi/middleware.RealIP in the
// chain. RealIP rewrites r.RemoteAddr based on the trusted
// proxy headers; reading the value downstream of that step
// gives us the real client IP rather than the proxy's. The
// chain order in NewServer reflects this.
//
// The middleware is intentionally cheap: a single SplitHostPort
// call and one context.WithValue per request. Operators can
// run it on every endpoint without measurable latency impact.
func clientIPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				// RemoteAddr may be just "host" without a
				// port — e.g., unix sockets, some test
				// fixtures, or requests through non-TCP
				// listeners. Record the raw value rather
				// than dropping the IP.
				host = r.RemoteAddr
			}
			ctx := context.WithValue(r.Context(), clientIPContextKey{}, host)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
