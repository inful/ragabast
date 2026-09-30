package web

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChatDarkThemeAssets pins the headline fix for issue #94:
// the web UI ships a small dark-mode affordance. The bundled
// Bulma is 0.x (no native data-theme support), so the dark
// surface is implemented as a small CSS override file the
// toggled <html data-theme="dark"> activates. The contract:
//   - chat-dark.css exists in the embedded static bundle
//   - the override targets the highest-impact surfaces
//     (background, text, box) so a real Bulma 0.x install
//     visibly changes
//   - chat.js wires the toggle button to <html data-theme="dark">
//     and persists the choice in localStorage so it survives
//     reloads
//
// Tests pin the assets' content rather than rendering CSS at
// runtime — the actual dark surface would need a browser. The
// goal is to lock the contract so a future contributor who
// removes the toggle or the override gets a red CI.
func TestChatDarkThemeAssets(t *testing.T) {
	darkCSS := readStaticAsset(t, "/static/chat-dark.css")
	require.NotEmpty(t, darkCSS,
		"chat-dark.css must exist so the dark-mode override can be toggled")

	// The override must scope by [data-theme="dark"] so it
	// only kicks in when the operator toggles the theme, not
	// bleed into the default light theme.
	require.Contains(t, darkCSS, `data-theme="dark"`,
		"chat-dark.css must scope its overrides by [data-theme=\"dark\"] so it doesn't pollute the default light theme")

	// Highest-impact surfaces the eye reads first. Each
	// assertion ensures a future contributor who simplifies
	// the override (e.g. drops background but keeps text) gets
	// a failing test rather than a half-broken dark mode.
	for _, prop := range []string{"background", "color"} {
		require.Contains(t, darkCSS, prop,
			"chat-dark.css must touch %q (got: %s)", prop, darkCSS)
	}

	// chat.js wires the click handler to toggle data-theme and
	// persists in localStorage. Pin both ends so a regression
	// either way shows up.
	js := readStaticAsset(t, "/static/chat.js")
	require.Contains(t, js, "data-theme",
		"chat.js must reference data-theme so the click handler can toggle <html data-theme=\"dark\">")
	require.Contains(t, js, "localStorage",
		"chat.js must persist the toggle in localStorage so the choice survives reloads")
}

// TestChatFallback_ThemeToggleInNavbar pins that the navbar
// carries the toggle button. After issue #85 the navbar is
// always rendered; this button sits in navbar-end alongside
// the auth bits when present. The test anchors on the id
// chat.js wires in its click handler.
func TestChatFallback_ThemeToggleInNavbar(t *testing.T) {
	require.Contains(t, pageHeaderFallbackBody, `id="theme-toggle"`,
		"the navbar must carry the theme toggle button so operators can flip between light and dark without touching the URL")
}
