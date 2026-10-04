package web

import (
	"net/http"
	"strings"
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
//
//   - X-Content-Type-Options: nosniff
//     Blocks browser MIME sniffing (defense against a
//     malicious upload being served as HTML).
//
//   - Referrer-Policy: no-referrer
//     Never leaks the URL bar to outbound links. chat and
//     search results can contain source URLs the user is
//     clicking away to; we don't want to leak the operator's
//     query string with that.
//
//   - X-Frame-Options: DENY
//     Belt-and-suspenders clickjacking protection even
//     though the CSP also forbids framing via frame-ancestors.
//
//   - Content-Security-Policy
//     default-src 'self'; script-src 'self'; style-src 'self';
//     img-src 'self' data:; frame-ancestors 'none';
//     base-uri 'self'; form-action 'self'.
//     Strictly self-hosted: daisyUI (CSS) and htmx (JS) ship
//     inside the binary via go:embed and are served from
//     /static/*. The historical build whitelisted
//     https://unpkg.com (htmx) and https://cdn.jsdelivr.net
//     (the pre-migration Bulma CSS); bundling those assets
//     lets the CSP drop those origins entirely, which is
//     strictly more secure. Adding a new external origin
//     means updating this CSP AND adding an SRI hash to
//     the <link>/<script> tag in the template that loads it.
//
//     The Huma-rendered OpenAPI viewer at /docs is the
//     single exception: Stoplight Elements loads its
//     stylesheet and web-component runtime from unpkg.com
//     (with an SRI hash pinned by the Huma library), so
//     /docs gets cspWithDocs — the strict CSP would block
//     the bundled viewer entirely. /docs is developer-only
//     and never serves user input, so the wider
//     trusted-script-origin set is contained to that
//     surface.
//
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
		// Per-route CSP: /docs (the Huma-rendered OpenAPI
		// viewer) whitelists unpkg.com so Stoplight Elements
		// can load; every other path gets the strict policy.
		// The /docs prefix match covers the docs HTML
		// itself; /openapi.{json,yaml} and /schemas/* are
		// JSON/YAML responses that don't execute script so
		// they correctly stay on cspStrict.
		if strings.HasPrefix(r.URL.Path, "/docs") {
			h.Set("Content-Security-Policy", cspWithDocs)
		} else {
			h.Set("Content-Security-Policy", cspStrict)
		}
		// max-age=63072000 is two years. Browsers ignore HSTS
		// over cleartext, so the header is harmless until TLS
		// is in front.
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")

		next.ServeHTTP(w, r)
	})
}

// cspStrict is the default Content-Security-Policy sent on
// every response except /docs. Kept as a package-level
// constant so the tests can reference the exact policy string
// and any future contributor can grep for it.
//
// The policy is strictly self-hosted: daisyUI (CSS) and htmx
// (JS) ship inside the binary via go:embed and are served
// from /static/*. No external host needs to be reachable for
// the page chrome to load.
//
// Adding a new external origin requires:
//  1. Updating this constant to allow the origin in the
//     relevant directive.
//  2. Adding an SRI hash (integrity + crossorigin attributes)
//     to the <link> or <script> tag in the template that
//     loads it. Without SRI, a CDN compromise runs arbitrary
//     JS in the operator's origin.
const cspStrict = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

// cspWithDocs is the looser CSP sent only for the Huma /docs
// route. The OpenAPI viewer embeds Stoplight Elements via
// <link> + <script> tags pointing at unpkg.com (with an SRI
// hash pinned by the Huma library, v2.34.x). cspStrict would
// block both, breaking the developer-facing API explorer.
//
// The relaxation is contained to /docs: the per-route check
// in securityHeadersMiddleware applies the looser policy
// only to requests whose URL path starts with /docs. Every
// other route, including /openapi.json and /openapi.yaml,
// stays on cspStrict. /docs does not render any user input
// (Huma serves a fixed template populated with the
// server-generated OpenAPI doc), so the wider
// trusted-script-origin set is bounded to a known surface.
//
// 'unsafe-inline' is allowed in style-src (Phase 9 of
// plans/ux-overhaul.md) because two pieces of the docs
// surface need it:
//  1. Huma's emitted template carries
//     `<body style="height: 100vh;">` verbatim — a
//     fixed inline style attribute.
//  2. `<elements-api>` is a Stoplight custom element
//     that generates dynamic inline `<style>` blocks
//     in its shadow DOM at runtime (to size the
//     expand/collapse / search / copy-as-cURL / lock
//     SVG icons, among other things). Without
//     'unsafe-inline' in style-src, the browser drops
//     those rules and the icons render at browser
//     default sizes (huge or invisible).
//
// The risk is bounded:
//   - script-src stays 'self' https://unpkg.com — no
//     remote code execution becomes available.
//   - 'unsafe-inline' for styles enables only CSS
//     injection vectors (data exfiltration via
//     background-image: url(...), clickjacking tricks);
//     cannot execute scripts.
//   - /docs does not render any user input, so the
//     attack surface for CSS injection is small.
const cspWithDocs = "default-src 'self'; " +
	"script-src 'self' https://unpkg.com; " +
	"style-src 'self' https://unpkg.com 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"
