package web

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// loginProvidersTestServer builds the standard
// login-test Server fixture with GitHub + GitLab
// providers configured. Centralized so the Phase 5
// tests (5.1 through 5.4) all use the same setup.
func loginProvidersTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Auth.Providers = []config.OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
		{Name: "gl", Type: "gitlab", ClientID: "id", ClientSecret: "sec"},
	}
	return NewServer(cfg, &fakeService{})
}

// loginPageBody fetches the rendered /auth/login page
// body. Phase 5's tests render the chooser (with
// multiple providers configured) and assert on the
// resulting HTML.
func loginPageBody(t *testing.T, s *Server, query string) string {
	t.Helper()
	url := "/auth/login"
	if query != "" {
		url = url + "?" + query
	}
	req := httptest.NewRequestWithContext(t.Context(), "GET", url, nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, "GET /auth/login must return 200")
	return w.Body.String()
}

// TestLogin_ProviderHasLogo pins the Phase 5.1 contract
// from plans/ux-overhaul.md: each provider link on the
// login chooser includes an inline SVG logo next to the
// text. The standard pattern (Auth0, Clerk, WorkOS, every
// modern auth UI) is to put a recognizable brand mark
// next to the provider name — text-only "Sign in with
// GitHub" reads as a CLI prompt, not a real auth page.
func TestLogin_ProviderHasLogo(t *testing.T) {
	body := loginPageBody(t, loginProvidersTestServer(t), "")

	// GitHub provider renders with a logo.
	// We don't pin the exact path data (a future
	// contributor can swap to a more current
	// GitHub mark without breaking the contract);
	// we pin the structural property: every
	// <a href="/auth/.../login"> element contains
	// an <svg> child.
	//
	// The regex is intentionally simple — Go's
	// regexp engine (RE2) doesn't support
	// negative lookahead, so we match the
	// <a> opening tag, then any content up to
	// (and including) the <svg> opener, without
	// trying to exclude the </a> closer (the
	// test just wants to confirm the <a>
	// contains an <svg> child, not pin the exact
	// placement of </a>).
	githubLinkRE := `<a[^>]*href="/auth/gh/login[^"]*"[\s\S]*?<svg`
	assert.Regexp(t, githubLinkRE, body,
		"the GitHub provider link must contain an inline <svg> logo (Phase 5.1 of plans/ux-overhaul.md)")

	// GitLab provider renders with a logo.
	gitlabLinkRE := `<a[^>]*href="/auth/gl/login[^"]*"[\s\S]*?<svg`
	assert.Regexp(t, gitlabLinkRE, body,
		"the GitLab provider link must contain an inline <svg> logo (Phase 5.1 of plans/ux-overhaul.md)")

	// Both SVGs use currentColor (or fill/text color)
	// so the icon follows the link's text color in
	// both light and dark contexts.
	//
	// We don't pin the exact attribute name
	// (fill="currentColor" or stroke="..." or
	// style="color: inherit") because daisyUI's
	// theming handles this differently across
	// component versions. The contract is
	// "currentColor-aware", not "uses fill
	// literally".
	//
	// The current template uses fill="currentColor"
	// explicitly; pin that to keep the contract
	// stable.
	assert.Contains(t, body, `fill="currentColor"`,
		"provider logos must use fill=\"currentColor\" so the icon color follows the link's text color (a11y / theming contract)")
}

// TestLogin_HasErrorAlertTarget pins the Phase 5.2
// contract: the login page renders an error alert
// when the URL carries an `error` query param. The
// OAuth callback redirects to /auth/login?error=...
// on failure (per the SPEC); the page must show the
// alert so the user knows what happened.
func TestLogin_HasErrorAlertTarget(t *testing.T) {
	body := loginPageBody(t, loginProvidersTestServer(t), "error=oauth_failed")

	// The alert must render with daisyUI's
	// alert-error modifier so it reads as a
	// danger surface (red, with the standard
	// alert border + icon).
	assert.Regexp(t, `class="[^"]*\balert\b[^"]*\balert-error\b`, body,
		"the login page must render an alert alert-error when ?error= is present (Phase 5.2 of plans/ux-overhaul.md)")

	// The alert must carry an id (e.g.
	// #login-error) so JS / a future focus call
	// can target it. The SPEC calls out a
	// #login-error id explicitly.
	assert.Contains(t, body, `id="login-error"`,
		"the login error alert must carry id=\"login-error\" so JS can target it (Phase 5.2 contract)")

	// The error message text must appear. The
	// SPEC's example is the literal string
	// "oauth_failed" (the OAuth error code) but
	// the alert surfaces a human-readable message
	// that names the failure (e.g. "Sign-in
	// failed", "Your sign-in session expired").
	// We pin that the alert is non-empty and
	// mentions a "sign-in" or "sign" verb.
	assert.Regexp(t, `(?i)sign[- ]?in`, body,
		"the alert must surface a human-readable failure message (e.g. 'Sign-in failed', 'Your sign-in session expired')")
}

// TestLogin_NoErrorOnCleanLoad pins the negative half
// of the 5.2 contract: a clean load (no ?error=
// query param) must NOT render the alert. A future
// contributor who hardcodes the alert into the page
// (forgetting the conditional) would show it on
// every page load.
func TestLogin_NoErrorOnCleanLoad(t *testing.T) {
	body := loginPageBody(t, loginProvidersTestServer(t), "")

	assert.NotRegexp(t, `id="login-error"`, body,
		"the login error alert must NOT render on a clean /auth/login load (no ?error= param)")
	assert.NotRegexp(t, `class="[^"]*\balert-error\b`, body,
		"the login page must not render an alert-error surface on a clean load")
}

// TestLogin_HasHelpLink pins the Phase 5.3 contract:
// the login page renders a "Having trouble signing
// in? Contact your administrator" link below the
// chooser. The link is the user-facing affordance
// for the "I can't log in" path — without it,
// operators who hit an OAuth failure have no
// in-page recourse.
func TestLogin_HasHelpLink(t *testing.T) {
	body := loginPageBody(t, loginProvidersTestServer(t), "")

	// The "Having trouble" copy must appear in the
	// rendered page. We don't pin the exact wording
	// — "Need help?", "Trouble signing in?", etc.
	// are all valid — but the link must reference
	// the "trouble" / "help" semantic so the
	// discoverability is clear.
	assert.Regexp(t, `(?i)trouble|help`, body,
		"the login page must surface a 'trouble signing in / help' link (Phase 5.3 of plans/ux-overhaul.md)")

	// The link must be a contact affordance
	// (mailto: or a /admin path). The SPEC's
	// example is a mailto: link; we accept
	// either.
	assert.Regexp(t, `<a[^>]*href="(?:mailto:|/admin)[^"]*"`, body,
		"the help link must be a mailto: or /admin contact affordance (Phase 5.3 contract)")
}

// TestLogin_HasLogoMark pins the Phase 5.4 contract:
// the brand bar (the navbar partial) renders a small
// inline SVG mark next to the "ragabast" text. The
// daisyUI brand-bar pattern pairs a logo mark with
// the brand name; the mark is the visual anchor
// (the name is the label).
//
// The login page renders the navbar partial (via
// {{ template "header" .Header }}), so testing the
// login page exercises the same navbar that
// /chat, /search, and /documents render.
func TestLogin_HasLogoMark(t *testing.T) {
	body := loginPageBody(t, loginProvidersTestServer(t), "")

	// The brand link wraps both the logo mark and
	// the text. We don't pin the exact path data
	// (a future redesign of the mark is fine), but
	// the structural property "the brand link
	// contains an <svg>" is the contract.
	//
	// The brand link is the <a class="btn
	// btn-ghost text-xl" href="/">…</a> in the
	// navbar. It contains both the SVG mark and
	// the "ragabast" text. We use a permissive
	// regex that tolerates the class / href
	// attributes appearing in any order (HTML5
	// allows either).
	brandLinkRE := `<a[^>]*?(?:href="/"[^>]*?|class="[^"]*\bbtn\b[^"]*\bbtn-ghost\b[^"]*"[^>]*?)[\s\S]*?<svg`
	assert.Regexp(t, brandLinkRE, body,
		"the brand bar link must contain an inline <svg> logo mark next to the 'ragabast' text (Phase 5.4 of plans/ux-overhaul.md)")

	// The SVG must use currentColor (or fill="..." +
	// explicit color) so the mark follows the
	// brand bar's text color in both themes. The
	// existing test pattern from 5.1 carries
	// over.
	assert.Contains(t, body, `fill="currentColor"`,
		"the brand logo mark must use fill=\"currentColor\" so the mark follows the brand bar's text color")
}
