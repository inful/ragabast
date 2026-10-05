package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChat_AutoScrollOnEveryNewMessage pins the
// "always scroll to bottom on new message" contract
// that replaced the Phase 2.5 "near-bottom check"
// behavior. The user reported that the near-bottom
// check left them stranded at a stale scroll position
// whenever a new message arrived while they were
// reading history — they didn't want to have to click
// "Jump to latest" to see the newest reply. The fix
// removes the gating and always scrolls the log to
// the bottom on every htmx:afterRequest.
//
// The behavior is wired in chat.js. The unconditional
// scroll assignment is the contract; the isNearBottom
// helper is still used (by the Jump-to-latest button
// visibility toggle) but it no longer gates the
// auto-scroll itself.
//
// Trade-off (called out so a future contributor
// doesn't re-introduce the gating on a hunch):
// always-scrolling means an operator scrolled up to
// read history will be scrolled away when a new
// message arrives. The Jump-to-latest button still
// appears when they manually scroll up after the
// fact, so they can get back to the tail with one
// click — but they will not be left there silently
// when a new message lands.
//
// chat.js installs TWO htmx:afterRequest listeners:
// the first clears the form's loading state and
// refocuses the input (a form-level concern); the
// second (inside the `if (log && jumpBtn)` block)
// scrolls the chat log to the bottom and toggles the
// Jump-to-latest button. The test scans both bodies
// and asserts that one of them does the auto-scroll
// without an isNearBottom gate.
func TestChat_AutoScrollOnEveryNewMessage(t *testing.T) {
	body := readStaticAsset(t, "/static/chat.js")

	// The htmx:afterRequest listener on the chat
	// form must still exist (regression guard) —
	// the listener is the same one that drove the
	// "near-bottom" auto-scroll, just without the
	// gating now.
	require.Contains(t, body, "htmx:afterRequest",
		"chat.js must install an htmx:afterRequest listener that drives the auto-scroll behavior")

	// The auto-scroll must assign to the chat
	// log's scrollTop. We don't pin the RHS — a
	// future contributor might store scrollHeight
	// in a variable for clarity (e.g. `var bottom =
	// log.scrollHeight; log.scrollTop = bottom;`)
	// without breaking the contract. The LHS is
	// the structural invariant.
	assert.Regexp(t, `\.scrollTop\s*=`, body,
		"chat.js must assign something to .scrollTop to scroll the chat log to the bottom on every new message")

	// Collect every htmx:afterRequest listener
	// body. The auto-scroll listener might be the
	// first, the second, or one of several — the
	// contract is "the auto-scroll is wired to
	// htmx:afterRequest, without an isNearBottom
	// gate", not "the first listener is the
	// auto-scroll listener".
	listenerBodies := extractAllHtmxAfterRequestBodies(body)
	require.NotEmpty(t, listenerBodies,
		"chat.js must install at least one htmx:afterRequest listener with a non-empty body")

	// At least one listener must do the
	// auto-scroll (assign to .scrollTop). The
	// listener that does the loading-state
	// housekeeping doesn't, but that's fine —
	// we only need at least one body that does.
	hasAutoScroll := false
	for _, b := range listenerBodies {
		if matched, _ := regexp.MatchString(`\.scrollTop\s*=`, b); matched {
			hasAutoScroll = true
			break
		}
	}
	assert.True(t, hasAutoScroll,
		"at least one htmx:afterRequest listener must assign to .scrollTop so the auto-scroll actually fires on every new message")

	// None of the htmx:afterRequest listeners
	// may gate on isNearBottom. The Jump-to-latest
	// button visibility is still driven by
	// isNearBottom (on a separate 'scroll' event
	// listener, not htmx:afterRequest) — that path
	// is unaffected. But if any htmx:afterRequest
	// listener ever reintroduces the
	// `if (isNearBottom(log)) { ... }` gate around
	// the scroll assignment, the user is back to
	// being stranded on a stale scroll position.
	for i, b := range listenerBodies {
		assert.NotContains(t, b, "isNearBottom",
			"htmx:afterRequest listener #%d must NOT gate the auto-scroll on an isNearBottom() check — every new message must scroll the log to the bottom so the user never has to click \"Jump to latest\" to see the newest reply", i)
	}
}

// TestChat_JumpToLatestButtonStillExists pins the
// manual-override surface: the chat landing page still
// renders a jump-to-latest button. With the new
// always-scroll behavior, the button's primary purpose
// is no longer "follow new messages" (the auto-scroll
// does that now) but "jump back to the bottom when the
// user has manually scrolled up to read history and
// wants to return to the tail." Without the button, an
// operator who scrolled up after the auto-scroll would
// have to scroll the log manually back to the bottom.
//
// The button is rendered by the chat fallback
// (chatFallbackBody) — same as before. The wiring in
// chat.js still toggles its visibility based on the
// isNearBottom() check on every scroll event.
func TestChat_JumpToLatestButtonStillExists(t *testing.T) {
	require.Contains(t, chatFallbackBody, `id="jump-to-latest"`,
		"the chat landing page must render a jump-to-latest button so the operator can jump back to the tail after manually scrolling up to read history")

	// The button must be a daisyUI btn so it picks
	// up the standard button styling.
	assert.Regexp(t, `<button[^>]*\bid="jump-to-latest"[^>]*class="[^"]*\bbtn\b`, chatFallbackBody,
		"the jump-to-latest button must use daisyUI's btn class")
}

// extractAllHtmxAfterRequestBodies returns the body
// of every `addEventListener('htmx:afterRequest', ...)`
// listener in chat.js, in source order. Returns an
// empty slice if no listener is found.
//
// chat.js installs at least one htmx:afterRequest
// listener (sometimes more than one — the form
// state, the chat-log scroll, the Jump-to-latest
// toggle, etc.). The contract is "the auto-scroll
// is wired to htmx:afterRequest, without an
// isNearBottom gate", so the test scans every
// listener body rather than the first one.
//
// chat.js uses `addEventListener('htmx:afterRequest',
// function () { ... });` for these listeners. The
// matching pins to the full call signature
// (`addEventListener('htmx:afterRequest'`) rather
// than the bare event name, because the bare name
// also appears in comments and the brace-counting
// pass would then point at the wrong listener. The
// signature is unambiguous because it only appears
// at actual registration sites.
//
// We brace-count from the opening `{` of the
// listener function to its matching `}`. chat.js has
// no nested function expressions or template
// literals inside the listener bodies, so a simple
// counter is sufficient.
func extractAllHtmxAfterRequestBodies(body string) []string {
	var bodies []string
	signatures := []string{
		"addEventListener('htmx:afterRequest'",
		`addEventListener("htmx:afterRequest"`,
	}
	for _, sig := range signatures {
		searchFrom := 0
		for {
			idx := strings.Index(body[searchFrom:], sig)
			if idx < 0 {
				break
			}
			idx += searchFrom
			// Walk forward to the next "function"
			// keyword after the event name.
			fnIdx := strings.Index(body[idx:], "function")
			if fnIdx < 0 {
				break
			}
			fnIdx += idx
			// Find the opening brace of the
			// function body.
			openBrace := strings.Index(body[fnIdx:], "{")
			if openBrace < 0 {
				break
			}
			openBrace += fnIdx
			depth := 0
			for i := openBrace; i < len(body); i++ {
				switch body[i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						bodies = append(bodies, body[openBrace+1:i])
						searchFrom = i + 1
						goto next
					}
				}
			}
			break
		next:
		}
	}
	return bodies
}
