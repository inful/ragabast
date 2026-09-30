package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChatFallback_LoadingStateAttributes pins the loading-state
// UX wiring for issue #92: the chat form must carry the three
// htmx attributes that drive (a) disabling the input + send
// button while the LLM request is in flight, and (b) revealing
// the "Thinking…" indicator inside the form. Without these,
// the user can submit a second message while the first is
// still in flight, which races the responses and produces
// interleaved replies in the wrong order.
//
// Strict CSP blocks the inline hx-on::* pattern that used to
// drive this UX; the attribute form below is the CSP-clean
// alternative (htmx honors them natively, no eval needed).
func TestChatFallback_LoadingStateAttributes(t *testing.T) {
	require.Contains(t, chatFallbackBody, `hx-disabled-elt="#chat-send, #chat-input"`,
		"the chat form must carry hx-disabled-elt so the input + send button can't fire a second request while the first is in flight (issue #92)")
	require.Contains(t, chatFallbackBody, `hx-indicator="#chat-indicator"`,
		"the chat form must carry hx-indicator so the Thinking\u2026 tag reveals itself while a request is in flight")
	require.Contains(t, chatFallbackBody, `id="chat-indicator"`,
		"the indicator element must carry the id the form points at")

	// chat.css carries the .htmx-indicator / .htmx-request
	// cascade that toggles the indicator's visibility. A
	// regression that drops either class silently breaks the
	// "Thinking…" affordance. Pin the CSS contract here so
	// the test fails before chrome-devops ever sees the UI.
	css := readStaticAsset(t, "/static/chat.css")
	require.Contains(t, css, ".htmx-indicator",
		"chat.css must define .htmx-indicator styles (otherwise the indicator never appears)")
	require.True(t, strings.Contains(css, ".htmx-request.htmx-indicator") ||
		strings.Contains(css, ".htmx-indicator { display: none"),
		"chat.css must hide .htmx-indicator by default and reveal it on .htmx-request; current rules: %s", css)

	// Issue #92 polish: the in-flight indicator must be
	// visually prominent — a small is-light tag was easy to
	// miss. Pin a spinner animation so a future contributor
	// who simplifies the CSS back to a quiet tag gets a
	// failing test rather than a silent UX regression.
	require.Contains(t, css, "@keyframes chat-indicator-spin",
		"chat.css must spin the in-flight indicator so the affordance reads at a glance")
}
