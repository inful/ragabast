package web

import (
	"net/http"
)

// securityHeadersMiddleware adds the defense-in-depth HTTP
// response headers every public route should carry.
//
// Why this lives here and not in each handler: a single
// middleware that fires before the route handler is the only
// way to guarantee the headers land on every response
// (including the Huma API JSON responses, the static stub,
// and any future endpoint a contributor adds). Pinning the
// headers in each handler is a maintenance trap and a
// guaranteed miss on the next "tiny" route someone wires up.
//
// Headers added:
//   - X-Content-Type-Options: nosniff
//     Blocks browser MIME sniffing (defense against a
//     malicious upload being served as HTML).
//   - Referrer-Policy: no-referrer
//     Never leaks the URL bar to outbound links. chat and
//     search results can contain source URLs the user is
//     clicking away to; we don't want to leak the operator's
//     query string with that.
//   - X-Frame-Options: DENY
//     Belt-and-suspenders clickjacking protection even
//     though the CSP also forbids framing via frame-ancestors.
//   - Content-Security-Policy
//     default-src 'self'; script-src 'self' https://unpkg.com;
//     style-src 'self' https://cdn.jsdelivr.net;
//     img-src 'self' data:; frame-ancestors 'none';
//     base-uri 'self'; form-action 'self'.
//     The two third-party origins are exactly the CDN URLs
//     the templates load Bulma (css) and htmx (js) from. Adding
//     a new external origin means updating this CSP AND
//     adding an SRI hash to the <link>/<script> tag.
//   - Strict-Transport-Security: max-age=63072000; includeSubDomains
//     Sent on every response. Browsers only act on HSTS over
//     HTTPS, so the header is a no-op when serving cleartext;
//     safe to send unconditionally.
//
// The middleware is intentionally read-only — it never
// mutates the request or short-circuits the chain.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", cspValue)
		// max-age=63072000 is two years. Browsers ignore HSTS
		// over cleartext, so the header is harmless until TLS
		// is in front.
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")

		next.ServeHTTP(w, r)
	})
}

// cspValue is the Content-Security-Policy sent on every
// response. Kept as a package-level constant so the tests can
// reference the exact policy string and any future contributor
// can grep for it.
//
// Adding a new external origin requires:
//  1. Updating this constant to allow the origin in the
//     relevant directive.
//  2. Adding an SRI hash (integrity + crossorigin attributes)
//     to the <link> or <script> tag in the template that
//     loads it. Without SRI, a CDN compromise runs arbitrary
//     JS in the operator's origin.
const cspValue = "default-src 'self'; " +
	"script-src 'self' https://unpkg.com; " +
	"style-src 'self' https://cdn.jsdelivr.net; " +
	"img-src 'self' data:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"
