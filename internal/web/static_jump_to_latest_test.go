package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChatAssets_JumpToLatestHelper pins the headline fix for
// issue #88: a "Jump to latest" button that appears when the
// user has scrolled away from the bottom of the chat log, so long
// sessions don't leave the operator stranded looking at a stale
// tail. The helper logic lives in chat.js (the bundled asset
// already wired to handle CSP-safe chat-form listeners); the
// static assertion below confirms the helper exists and is wired
// to the chat-log container, the swap-in event, and a scroll
// listener. Together with TestChatFallback_IncludesJumpToLatest
// and TestChatCSS_JumpToLatestStyles, this pins the three
// surfaces the feature touches.
func TestChatAssets_JumpToLatestHelper(t *testing.T) {
	body := readStaticAsset(t, "/static/chat.js")

	require.Contains(t, body, "chat-log",
		"chat.js must read the chat-log container so it knows what to observe")
	require.Contains(t, body, "jump-to-latest",
		"chat.js must reference the jump-to-latest button so it can toggle visibility and scroll")
	require.Contains(t, body, "scrollTop",
		"chat.js must use scrollTop to detect / restore the scroll position")
	require.Contains(t, body, "htmx:afterRequest",
		"chat.js must auto-scroll on htmx:afterRequest so the operator lands on the new message")
}

// (No "no inline scripts" assertion here on purpose — the
// existing chat.js header comment names the historical hx-on
// attributes so a future contributor understands why the file
// exists. An assertion that no string contains "hx-on:" would
// be brittle. The CSP invariant is enforced at the HTTP
// header layer, not by JS greps.)

// TestChatFallback_IncludesJumpToLatest pins that the chat
// landing page (rendered by the Go-string fallback) carries the
// jump-to-latest button. The button is a child of the chat-log
// container so it scrolls with the log and is absolutely
// positioned to the bottom-right corner via CSS.
func TestChatFallback_IncludesJumpToLatest(t *testing.T) {
	require.Contains(t, chatFallbackBody, `id="jump-to-latest"`,
		"the chat landing page must render a jump-to-latest button so long sessions have a way back to the tail")
	require.Contains(t, chatFallbackBody, `class="button is-small jump-to-latest`,
		"the button must carry the .jump-to-latest class so chat.css can position it bottom-right and toggle visibility")
}

// TestChatCSS_JumpToLatestStyles pins the CSS surface: the button
// is hidden by default and absolutely-positioned to the
// bottom-right corner of the chat-log container, with a
// .is-visible class the JS toggles to show it when the user
// has scrolled away from the tail.
func TestChatCSS_JumpToLatestStyles(t *testing.T) {
	body := readStaticAsset(t, "/static/chat.css")

	require.Contains(t, body, ".jump-to-latest",
		"chat.css must define styles for the .jump-to-latest button")
	require.Contains(t, body, "display: none",
		"the button must be hidden by default — visibility is opt-in via the .is-visible class the JS toggles")
}

// readStaticAsset reads a bundled static asset out of the
// embedded FS so per-asset tests can grep for the behaviors
// they care about without standing up the full HTTP stack.
// The embed directive is `static/*`, so the files live under
// the `static/` prefix inside the FS — callers pass the URL
// path (e.g. /static/chat.js) and we strip /static/ then prepend
// static/.
func readStaticAsset(t *testing.T, path string) string {
	t.Helper()
	rel := strings.TrimPrefix(path, "/static/")
	data, err := staticFS.ReadFile("static/" + rel)
	require.NoError(t, err, "reading %s", path)
	return string(data)
}
