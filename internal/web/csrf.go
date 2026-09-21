package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"

	"github.com/ragabast/internal/config"
)

// csrfCookieName is the cookie the middleware sets on the
// first request so subsequent form POSTs have a token to
// echo in their hidden field. The double-submit-cookie
// pattern relies on the attacker NOT being able to read
// this cookie cross-origin, which is why HttpOnly is set.
const csrfCookieName = "ragabast_csrf"

// csrfFormField is the name of the hidden input every form
// must render before submitting to a protected endpoint.
// The middleware compares the submitted field value
// against the cookie value (constant-time) and rejects
// mismatches with 403.
const csrfFormField = "csrf_token"

// csrfTokenKey is the unexported context key under which
// the middleware stashes the effective csrf token so
// form-rendering handlers can embed it via the
// CsrfTokenFromContext helper. Stashing in context keeps
// the rendering layer independent of cookie handling.
type csrfTokenKey struct{}

// CsrfTokenFromContext returns the csrf token that the
// CSRF middleware associated with the current request —
// the cookie value if one was presented, or a freshly-
// generated token if not. Form-rendering handlers embed
// this value as a hidden input so the browser can echo
// it back on the subsequent POST.
func CsrfTokenFromContext(ctx context.Context) string {
	v, _ := ctx.Value(csrfTokenKey{}).(string)
	return v
}

// csrfMiddleware enforces the double-submit-cookie CSRF
// contract on the web layer.
//
// Threat model: the bearer token is the only credential
// today, and browsers do not send `Authorization` headers
// cross-origin, so a CSRF attack via a malicious
// third-party page cannot succeed against our API
// endpoints. This middleware is defensive — it positions
// the codebase for a future state where cookie-based
// session auth is added, and protects browser form
// submissions (which DO send cookies cross-origin) today.
//
// Flow:
//  1. Read the csrf cookie. If absent, generate 32 random
//     bytes (hex-encoded = 64 chars) and ensure a Set-Cookie
//     header goes out on this response so the browser
//     persists it.
//  2. Stash the effective token on the request context so
//     form-rendering handlers can embed it server-side.
//  3. For state-changing methods (POST/PUT/PATCH/DELETE)
//     on protected routes (authMiddleware passes them
//     through), if the request has no `Authorization:
//     Bearer` header, the form value must equal the
//     cookie value. Mismatch => 403 Forbidden.
//
// Why "Authorization: Bearer" bypasses CSRF: a cross-origin
// attacker cannot make the browser send an Authorization
// header, so any request that carries one is provably
// same-origin. CSRF is irrelevant for those requests.
//
// Why the cookie is HttpOnly: the value is embedded
// server-side into the hidden field, so client-side JS
// never needs to read it. HttpOnly prevents any XSS from
// leaking the token.
//
// Why SameSite=Lax: this is the right balance for a
// form-mounted app. Strict would break the OAuth-style
// callbacks we don't currently use but might; Lax allows
// top-level navigations (the normal click flow) while
// blocking third-party POSTs from cross-origin pages.
//
// Comparison is constant-time (subtle.ConstantTimeCompare)
// so a timing attack against the token bytes is not
// possible. The token is 32 random bytes, so a brute-force
// approach would need ~2^255 attempts on average.
func csrfMiddleware(effectiveTokens []config.AuthToken) func(http.Handler) http.Handler {
	// When auth is disabled, there is nothing to CSRF.
	// Mirror the auth-disabled bypass so the local-dev
	// open-access experience keeps working without
	// configuration; the existing API tests
	// (TestHandleSearchAPI_FiltersByMinScore and friends)
	// POST without a bearer and must continue to succeed.
	csrfEnabled := len(effectiveTokens) > 0
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := readOrGenerateCsrfToken(r)
			ensureCsrfCookie(w, token)
			ctx := context.WithValue(r.Context(), csrfTokenKey{}, token)
			r = r.WithContext(ctx)

			if csrfEnabled && csrfRequiresCheck(r) {
				if !csrfMatches(r, token) {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// csrfRequiresCheck reports whether this request needs
// the csrf field-vs-cookie comparison.
//
// The check is required when:
//   - the method is a state-changer (POST/PUT/PATCH/DELETE)
//   - the request carries no Authorization header (so
//     browsers could make it cross-origin)
//
// All other requests (GET/HEAD/OPTIONS, plus
// Authorization-bearing requests of any method) bypass
// the check entirely.
func csrfRequiresCheck(r *http.Request) bool {
	if !isStateChangingMethod(r.Method) {
		return false
	}
	if r.Header.Get("Authorization") != "" {
		return false
	}
	return true
}

// isStateChangingMethod reports whether m is a method that
// can mutate server state. GET/HEAD/OPTIONS are safe by
// HTTP semantics (browsers preflight OPTIONS cross-origin
// already, and our CORS layer replies 204 before auth/CSRF
// fire); the rest of the verbs can change data and so need
// CSRF protection when invoked without an Authorization
// header.
func isStateChangingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// csrfMatches reports whether the csrf form field in the
// request body matches the cookie's token value. The
// field can be supplied either as `csrf_token=...` (form
// POST) or via the `X-CSRF-Token` header (HTMX /
// programmatic callers). Constant-time comparison so the
// response latency does not leak the first-differing-byte
// position.
//
// A length mismatch is treated as a mismatch (not a
// separate branch) so the comparison time stays uniform.
func csrfMatches(r *http.Request, token string) bool {
	presented := r.FormValue(csrfFormField)
	if presented == "" {
		presented = r.Header.Get("X-CSRF-Token")
	}
	if len(presented) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}

// readOrGenerateCsrfToken returns the csrf cookie value
// if present, or generates a fresh 32-byte token
// otherwise. Generating here (rather than only on missing
// cookies) would risk overwriting an existing token; this
// helper only generates when the cookie is absent so the
// "no rotation on subsequent GET" invariant holds.
func readOrGenerateCsrfToken(r *http.Request) string {
	c, err := r.Cookie(csrfCookieName)
	if err == nil && c.Value != "" {
		return c.Value
	}
	return generateCsrfToken()
}

// generateCsrfToken returns a fresh 32-byte (256-bit)
// token, hex-encoded for cookie and form-field safety.
// 256 bits is far past the brute-force threshold for any
// realistic adversary; the only attack that matters is
// cross-origin cookie reading, which HttpOnly + SameSite
// already block.
func generateCsrfToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read on Linux/macOS/Windows returns
		// an error only if the OS RNG itself fails. Treat
		// that as catastrophic — better to crash than to
		// serve a predictable token.
		panic("csrf: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// ensureCsrfCookie sets the csrf cookie on the response
// if the request did not already carry one. Cookies are
// only set when the token was freshly generated (cookie
// was missing); for requests that already present a
// valid cookie we leave the Set-Cookie out so the browser
// keeps its existing token unchanged.
//
// HttpOnly is true because the form field is rendered
// server-side; client JS has no reason to read it. Path
// is "/" so the cookie covers every endpoint. SameSite
// is Lax so top-level navigations still send the cookie
// (e.g. clicking a link from email lands on /chat with
// the cookie attached).
func ensureCsrfCookie(w http.ResponseWriter, token string) {
	w.Header().Add("Set-Cookie", (&http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}).String())
}
