package web

import (
	"context"
	"net"
	"net/http"
	"strings"
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
// (already adjusted by safeRealIP for X-Forwarded-For / X-Real-IP)
// and stores it on the request context.
//
// Install this middleware AFTER safeRealIP in the chain.
// safeRealIP rewrites r.RemoteAddr based on the trusted
// proxy headers (loopback-only — see safeRealIP godoc);
// reading the value downstream of that step gives us the
// real client IP rather than the proxy's. The chain order
// in NewServer reflects this.
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

// safeRealIP replaces chi's deprecated middleware.RealIP with
// an in-repo equivalent that publishes the
// security/usability trade-off in code.
//
// Difference from chi.middleware.RealIP:
//   - Trust is gated on r.RemoteAddr being a loopback address.
//     The typical ragabast deployment is "reverse proxy on
//     localhost" — XFF is trustworthy in that case. A
//     hostile client connecting directly to ragabast (no
//     proxy in front) cannot forge XFF into r.RemoteAddr
//     because the connection's TCP peer is the malicious
//     client itself, not the loopback address — the
//     header is silently ignored.
//   - Only the leftmost XFF entry is considered (same as
//     chi's behavior). X-Real-IP is checked when XFF is
//     absent.
//
// What this commit does NOT fix:
//   - Multi-hop proxy chains (CDN -> nginx -> ragabast)
//     where the intermediate hop is non-loopback. That
//     requires a configured trusted-proxy list and is a
//     separate audit topic.
//
// Audit finding 8.
func safeRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			// Direct connection — trust nothing but the
			// TCP peer address. A hostile client can spoof
			// XFF freely without a proxy in front; the
			// spoofing is harmless because we drop it.
			next.ServeHTTP(w, r)
			return
		}

		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				xff = xff[:i]
			}
			client := strings.TrimSpace(xff)
			if client != "" {
				r.RemoteAddr = client + ":0"
			}
			next.ServeHTTP(w, r)
			return
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			r.RemoteAddr = strings.TrimSpace(xri) + ":0"
		}
		next.ServeHTTP(w, r)
	})
}
