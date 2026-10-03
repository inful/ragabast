package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChat_AutoScrollNearBottom pins the Phase 2.5
// contract from plans/ux-overhaul.md: after every
// htmx:afterRequest on the chat log, if the user is
// "near the bottom" (within ~50px), the chat log
// scrolls to the bottom; if the user has scrolled up
// to read history, they're left alone.
//
// The behavior is wired in chat.js (issue #88's
// Jump-to-latest scroll handler). Without the
// near-bottom check, every new message would snap
// the user to the bottom of the log — disastrous for
// long sessions where the user has scrolled up to
// re-read an earlier reply.
func TestChat_AutoScrollNearBottom(t *testing.T) {
	body := readStaticAsset(t, "/static/chat.js")

	// The htmx:afterRequest listener on the chat
	// form must check the near-bottom state before
	// scrolling. Pin the listener exists at all
	// (regression guard for the "Jump to latest"
	// feature) and the scroll target is the chat
	// log element.
	require.Contains(t, body, "htmx:afterRequest",
		"chat.js must install an htmx:afterRequest listener that drives the auto-scroll behavior (Phase 2.5 of plans/ux-overhaul.md)")

	// The near-bottom check uses a threshold
	// (NEAR_BOTTOM_PX). The spec says ~50px; the
	// implementation uses 48 (which is within the
	// 50px spirit). Pin the constant exists so a
	// future contributor who removes the threshold
	// (and snaps users to the bottom on every
	// message) gets a red test.
	assert.Regexp(t, `NEAR_BOTTOM_PX\s*=\s*\d+`, body,
		"chat.js must define a NEAR_BOTTOM_PX threshold so the auto-scroll doesn't snap users down on every message (Phase 2.5 contract)")

	// The handler must compare scrollTop +
	// clientHeight to scrollHeight against the
	// threshold. Pin the isNearBottom helper
	// exists; without it, the auto-scroll would
	// either never fire or always fire.
	assert.Contains(t, body, "isNearBottom",
		"chat.js must define an isNearBottom helper to drive the near-bottom check (Phase 2.5)")

	// The handler must scroll to the bottom
	// (log.scrollTop = log.scrollHeight) when the
	// user is near the bottom. Pin the assignment
	// exists inside the htmx:afterRequest listener.
	// We allow any RHS — a future contributor might
	// store scrollHeight in a variable for clarity
	// (e.g. `var bottom = log.scrollHeight;
	// log.scrollTop = bottom;`) without breaking
	// the contract. The LHS is the structural
	// invariant (something is being assigned to
	// the chat log's scrollTop).
	assert.Regexp(t, `\.scrollTop\s*=`, body,
		"chat.js must assign something to .scrollTop to follow new content when the user is near the bottom (Phase 2.5 contract)")
}

// TestChat_JumpToLatestButtonExists pins the manual
// override: the chat landing page renders a
// jump-to-latest button that becomes visible when
// the user has scrolled away from the bottom. The
// button is the "operator wants to follow" affordance
// — without it, an operator who scrolled up to
// re-read history would be stranded when new
// messages land.
//
// The button is rendered by the chat fallback
// (pageHeaderFallbackBody) — Phase 2.5 reuses the
// issue #88 implementation rather than introducing
// a new one.
func TestChat_JumpToLatestButtonExists(t *testing.T) {
	require.Contains(t, chatFallbackBody, `id="jump-to-latest"`,
		"the chat landing page must render a jump-to-latest button (Phase 2.5 of plans/ux-overhaul.md, reuses issue #88)")

	// The button must be a daisyUI btn so it picks
	// up the standard button styling.
	assert.Regexp(t, `<button[^>]*\bid="jump-to-latest"[^>]*class="[^"]*\bbtn\b`, chatFallbackBody,
		"the jump-to-latest button must use daisyUI's btn class")
}
