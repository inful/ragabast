package web

// Phase 8 of plans/ux-overhaul.md (login theme follow-on).
//
// Phase 5 added daisyUI utility classes to
// templates/login.html (e.g. `flex items-center gap-3
// border border-[#30363d] bg-[#161b22] size-5 shrink-0`)
// but never added `<link rel="stylesheet" href="...
// daisyui.min.css">` to the login page's <head>.
// Result: every daisyUI utility class on the login page
// is a hollow contract — markup says "size-5" but the
// CSS rule that applies it isn't loaded, and the SVGs
// render at intrinsic viewBox dimensions, dwarfing the
// page.
//
// The fix:
//   1. Add daisyui.min.css to the login page's <head>.
//   2. Replace the hardcoded GitHub-dark color tokens
//      (`border-[#30363d]`, `bg-[#161b22]`, `text-[#e6edf3]`,
//      `text-[#8b949e]`, `text-[#6e7681]`, `text-[#7d8590]`)
//      with theme-aware daisyUI classes
//      (`bg-base-200`, `text-base-content`,
//      `text-base-content/70`, `border-base-300`, etc.)
//      so the page follows `prefers-color-scheme`.
//   3. Drop the hardcoded `body { background: #0e1116;
//      color: #e6edf3 }` from login.css so the page
//      inherits daisyUI's themed `bg-base-100` /
//      `text-base-content`.
//
// This file pins all three contracts.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogin_LoadsDaisyUIBundle pins the "missing
// stylesheet" root cause. Without daisyui.min.css every
// utility class on the login page is a hollow contract;
// the SVGs render at intrinsic size (hundreds of pixels)
// instead of the intended 20x20 size-5.
func TestLogin_LoadsDaisyUIBundle(t *testing.T) {
	body := loginPageBody(t, loginProvidersTestServer(t), "")

	assert.Regexp(t, `href="/static/daisyui\.min\.css[^"]*"`,
		body,
		"the login page must load /static/daisyui.min.css so the daisyUI utility classes (size-5, flex, gap-3, ...) are actually styled (Phase 8 login themes)")
}

// TestLogin_NoHardcodedGitHubDarkTokens pins the
// "replace hardcoded color tokens with theme-aware
// classes" contract. The pre-Phase-8 login template
// shipped six hardcoded GitHub-dark color tokens that
// pinned the page to a dark palette regardless of
// `prefers-color-scheme`. Phase 8 replaces them with
// theme-aware daisyUI classes so the page follows the
// user's OS theme preference.
func TestLogin_NoHardcodedGitHubDarkTokens(t *testing.T) {
	// Comments in the Go template are passed through
	// to the rendered HTML, so we strip them first
	// — the test would otherwise trip on its own
	// documentation that names the removed tokens.
	body := stripHTMLComments(loginPageBody(t, loginProvidersTestServer(t), ""))

	// The pre-Phase-8 hardcoded color tokens.
	// Pin that NONE of them appear in the rendered
	// login HTML — they have all been replaced by
	// theme-aware daisyUI classes.
	hardcodedTokens := []string{
		"#30363d", // GitHub-dark border color
		"#161b22", // GitHub-dark panel background
		"#e6edf3", // GitHub-dark foreground text
		"#8b949e", // GitHub-dark muted text
		"#6e7681", // GitHub-dark helper text
		"#7d8590", // GitHub-dark helper text (variant)
	}
	for _, token := range hardcodedTokens {
		assert.NotContains(t, body, token,
			"the login page must not hardcode GitHub-dark color %s — use theme-aware daisyUI classes (bg-base-100, text-base-content, border-base-300, ...) so the page follows prefers-color-scheme (Phase 8 login themes)", token)
	}
}

// TestLogin_UsesThemeAwareClasses pins the positive
// counterpart: the login template now applies daisyUI's
// theme-aware color tokens. The daisyUI base palette is
// `bg-base-100` (background), `text-base-content`
// (foreground), `bg-base-200` (panel),
// `border-base-300` (border), etc. Pin that at least
// one of each shows up so a future contributor
// reverting to a hardcoded palette trips a test rather
// than silently regressing the theme contract.
func TestLogin_UsesThemeAwareClasses(t *testing.T) {
	body := loginPageBody(t, loginProvidersTestServer(t), "")

	mustContain := []string{
		// Surface background for the page or its
		// panels. Either the body or any of the
		// panels carries bg-base-100 / bg-base-200
		// (theme-aware).
		"bg-base-100",
		// Foreground text — the daisyUI theme's
		// "body text" token.
		"text-base-content",
	}
	for _, fragment := range mustContain {
		assert.Contains(t, body, fragment,
			"the login page must use theme-aware daisyUI classes (%s) so the page follows prefers-color-scheme (Phase 8 login themes)", fragment)
	}
}

// TestLoginCSS_NoHardcodedBodyBackground pins the
// "remove the hardcoded body background from login.css"
// contract. Pre-Phase-8 login.css pinned body to
// `background: #0e1116; color: #e6edf3` (the
// "always-dark" design). Phase 8 removes those
// declarations so the body inherits daisyUI's themed
// bg-base-100 / text-base-content. We assert on
// login.css directly so the test catches a
// regression even if no rendered HTML path reads
// the CSS.
func TestLoginCSS_NoHardcodedBodyBackground(t *testing.T) {
	// Strip CSS comments first — the test would
	// otherwise trip on its own documentation
	// that names the removed tokens.
	css := stripCSSComments(readStaticAsset(t, "/static/login.css"))
	require.NotEmpty(t, css, "login.css must be readable from the embedded bundle")

	// Pin that login.css does NOT pin the body
	// to a hardcoded dark background. We assert
	// via substring absence — a future
	// contributor re-adding `background: #0e1116`
	// (or any other explicit dark hex) trips
	// the test.
	hardcodedBackgrounds := []string{
		"#0e1116", // pre-Phase-8 dark page background
		"#161b22", // pre-Phase-8 dark panel background
	}
	for _, hex := range hardcodedBackgrounds {
		assert.NotContains(t, css, hex,
			"login.css must not hardcode %s as a background — let daisyUI's theme provide the themed background (Phase 8 login themes)", hex)
	}

	// Pin the positive shape: login.css still
	// targets `body` (it's still the page-chrome
	// file), but the body block only sets
	// typography + layout — NOT color.
	//
	// We accept either:
	//   - No body block at all (theme drives it)
	//   - A body block without `background:` or `color:`
	bodyIdx := strings.Index(css, "body")
	if bodyIdx >= 0 {
		// Snip out the body block (from "body" to the
		// next "}" at the same brace level) so the
		// assertion can check it doesn't pin color.
		block := bodyBlock(css, bodyIdx)
		assert.NotContains(t, block, "background:",
			"login.css body block must not pin background — let daisyUI's theme provide it (Phase 8 login themes)")
		assert.NotContains(t, block, "color:",
			"login.css body block must not pin color — let daisyUI's theme provide it (Phase 8 login themes)")
	}
}

// bodyBlock returns the body{...} block at or after
// `startIdx` in `css`. It walks forward from startIdx
// to the matching closing brace and returns the slice.
// If no body block is found, returns "".
func bodyBlock(css string, startIdx int) string {
	open := strings.Index(css[startIdx:], "{")
	if open < 0 {
		return ""
	}
	open += startIdx
	depth := 0
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[open : i+1]
			}
		}
	}
	return ""
}

// stripHTMLComments removes HTML comment blocks `<!-- ... -->`
// from `html`. Tests use it so that documentation comments
// in templates don't trip substring assertions on
// hex codes / class names they reference for context.
func stripHTMLComments(html string) string {
	var out strings.Builder
	i := 0
	for i < len(html) {
		// Find the next "<!--" opener.
		start := strings.Index(html[i:], "<!--")
		if start < 0 {
			out.WriteString(html[i:])
			return out.String()
		}
		out.WriteString(html[i : i+start])
		j := i + start + len("<!--")
		end := strings.Index(html[j:], "-->")
		if end < 0 {
			// Unterminated comment — drop the rest.
			return out.String()
		}
		j += end + len("-->")
		i = j
	}
	return out.String()
}

// stripCSSComments removes CSS block comments `/* ... */`
// from `css`. Same rationale as stripHTMLComments above.
func stripCSSComments(css string) string {
	var out strings.Builder
	i := 0
	for i < len(css) {
		start := strings.Index(css[i:], "/*")
		if start < 0 {
			out.WriteString(css[i:])
			return out.String()
		}
		out.WriteString(css[i : i+start])
		j := i + start + len("/*")
		end := strings.Index(css[j:], "*/")
		if end < 0 {
			// Unterminated comment — drop the rest.
			return out.String()
		}
		j += end + len("*/")
		i = j
	}
	return out.String()
}
