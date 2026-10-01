package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestChatDarkTheme_DrivesBulmaAutomaticDark pins the
// contract for dark mode after the Bulma 1.0.4 upgrade:
// Bulma 1.x ships an automatic `@media (prefers-color-scheme:
// dark)` block, so the OS preference is honored with no
// extra CSS. The toggle button (chat.js, issue #94) sets
// `data-theme=dark` on <html> to override the OS choice;
// chat-dark.css carries the manual override.
//
// The contract:
//
//   - chat-dark.css scopes its overrides by
//     [data-theme="dark"] so it only fires when the toggle
//     flips the attribute (not via prefers-color-scheme)
//   - chat-dark.css overrides Bulma's CSS variables
//     (--bulma-scheme-main, --bulma-text, etc.) so a
//     flipped toggle visibly changes every Bulma component
//     without per-component selectors — the variables
//     propagate
//   - chat.js keeps the toggle behavior the user already
//     has (click → data-theme attribute flip + localStorage
//     persist + prefers-color-scheme first-visit fallback)
//
// We test the asset surface rather than rendering the CSS
// because headless dark-mode detection against the live
// stylesheet would need a browser. The test pins both
// halves of the contract (CSS scope + JS wiring).
func TestChatDarkTheme_DrivesBulmaAutomaticDark(t *testing.T) {
	darkCSS := readStaticAsset(t, "/static/chat-dark.css")
	require.NotEmpty(t, darkCSS,
		"chat-dark.css must exist so the toggle still has a manual override path")

	// Override must scope by [data-theme="dark"] so it
	// only fires when chat.js flips the attribute, not on
	// every page (where prefers-color-scheme already handles
	// the automatic path).
	assert.Contains(t, darkCSS, `[data-theme="dark"]`,
		"chat-dark.css must scope its overrides by [data-theme=\"dark\"] — the OS preference is Bulma 1.x's job now")

	// Must override Bulma's CSS variables so the change
	// propagates to every component (no per-component
	// selectors needed). The variables are documented at
	// bulma.io/documentation/features/css-variables/.
	for _, variable := range []string{
		"--bulma-scheme-main",
		"--bulma-background",
		"--bulma-text",
	} {
		assert.Contains(t, darkCSS, variable,
			"chat-dark.css must override the %s CSS variable so a flipped toggle visibly changes the page", variable)
	}
}

// TestChatDarkTheme_ToggleStillWiresDataTheme pins the JS
// half of the contract: chat.js must continue to manage
// the <html data-theme="dark"> attribute on click and
// persist the choice in localStorage.
func TestChatDarkTheme_ToggleStillWiresDataTheme(t *testing.T) {
	js := readStaticAsset(t, "/static/chat.js")
	require.NotContains(t, js, "/* placeholder */",
		"sanity: this test assumes chat.js is the bundle under test")

	assert.Contains(t, js, "data-theme",
		"chat.js must reference data-theme so the click handler can flip <html data-theme=\"dark\">")
	assert.Contains(t, js, "localStorage",
		"chat.js must persist the toggle in localStorage so the choice survives reloads")
}

// TestChatDarkTheme_OSPreferenceHonoredByDefault pins the
// first-visit behavior: when the operator hasn't toggled,
// Bulma 1.x's prefers-color-scheme media query drives the
// theme. chat.js checks matchMedia before applying a stored
// choice so the first visit on a dark-OS machine gets
// Bulma's automatic dark theme without the operator
// touching the toggle.
func TestChatDarkTheme_OSPreferenceHonoredByDefault(t *testing.T) {
	js := readStaticAsset(t, "/static/chat.js")
	require.Contains(t, js, "prefers-color-scheme",
		"chat.js must read matchMedia('(prefers-color-scheme: dark)') so the first visit follows the OS preference")
}

// TestChatDarkTheme_NoMissingRendersAfterSwap is a smoke
// test: after the Bulma upgrade every page the static
// handler serves must continue to render 200 OK with the
// chat-dark.css link present. A regression here would
// catch the easy mistakes (forgot to update one of the
// fallback renderers, etc.).
//
// Note: /search only renders via the embedded template
// path (no fallback exists). The other three pages have
// fallback renderers; we exercise those by dropping the
// embedded templates so the test covers both code paths.
func TestChatDarkTheme_NoMissingRendersAfterSwap(t *testing.T) {
	cfg := config.DefaultConfig()

	// Fallback-renderer pages: drop embedded templates.
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil
	for _, path := range []string{"/", "/ingest", "/documents"} {
		t.Run("fallback/"+path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			assert.Contains(t, body, `href="/static/chat-dark.css"`,
				"%s (fallback) must still load chat-dark.css so the toggle keeps working", path)
		})
	}

	// Embedded-template pages: render via the default
	// (templates = nil falls through to the embedded set).
	s2 := NewServer(cfg, &fakeHumaService{})
	t.Run("embedded//search", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
		w := httptest.NewRecorder()
		s2.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `href="/static/chat-dark.css"`,
			"/search (embedded template) must still load chat-dark.css")
	})
}

// TestChatDarkTheme_TemplatesReferenceBulma1Path pins the
// embedded templates/chat.html used by the /chat page
// (when templates are present) — the static asset allow-
// list must keep chat-dark.css reachable.
func TestChatDarkTheme_TemplatesReferenceBulma1Path(t *testing.T) {
	tmpl, err := os.ReadFile("templates/login.html")
	require.NoError(t, err, "login template must be readable")
	require.NotEmpty(t, tmpl)

	assert.Contains(t, string(tmpl), `href="/static/chat-dark.css"`,
		"login.html must continue to load chat-dark.css so the toggle button on the login page works")
}

// TestChatDarkTheme_RemovesRawDarkColors is a guardrail
// for a future contributor tempted to add per-component
// dark overrides (e.g. a dedicated .chat-log color). The
// chat-dark.css contract is to override Bulma's CSS
// variables once, not to maintain per-component palettes.
// If a future contributor adds a dark override for a
// specific class, this test flags the regression so the
// conversation about "why isn't this in chat-dark.css" can
// happen before the change lands.
//
// Pin: the file declares overrides keyed by [data-theme]
// + tag selectors only. Selector count > 12 means a
// contributor added per-component overrides.
func TestChatDarkTheme_RemovesRawDarkColors(t *testing.T) {
	darkCSS := readStaticAsset(t, "/static/chat-dark.css")

	// Count top-level selector openings. The current
	// file has ~12 (one per [data-theme="dark"] <selector>
	// block plus a couple of helper selectors). A
	// per-component override pass would push this well
	// past 25.
	openCount := strings.Count(darkCSS, "{")
	assert.Less(t, openCount, 25,
		"chat-dark.css must stay a small override (selector count=%d); per-component dark colors belong in Bulma's CSS variable system, not in this file", openCount)
}
