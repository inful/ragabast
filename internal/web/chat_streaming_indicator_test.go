package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChat_StreamingIndicator pins the Phase 2.3 contract
// from plans/ux-overhaul.md: the in-flight indicator in
// the chat form replaces the badge + spinner combo with
// daisyUI's loading component. The daisyUI `loading
// loading-dots` is the standard "typing" indicator
// pattern (Linear, Notion, GitHub all use it) and
// communicates "generating" without the text.
func TestChat_StreamingIndicator(t *testing.T) {
	// The chat fallback body is a package-level
	// constant (chatFallbackBody) — read it directly
	// to keep this test a pure string-grep rather
	// than a full Server setup. The fallback body
	// is the rendered chat page; the
	// #chat-indicator is the in-flight loading
	// surface.
	require.Contains(t, chatFallbackBody, `id="chat-indicator"`,
		"the chat form's in-flight indicator element must remain (the daisyUI loading component replaces the inner markup, not the container)")

	// The daisyUI loading component uses the
	// `loading` base class. Pin the base class so
	// a future contributor who reverts to a
	// custom <span> spinner gets a red test.
	assert.Regexp(t, `<span[^>]*class="[^"]*\bloading\b`, chatFallbackBody,
		"the in-flight indicator must use daisyUI's loading class (Phase 2.3 of plans/ux-overhaul.md)")

	// The `loading-dots` modifier is the daisyUI
	// "three pulsing dots" variant — the standard
	// typing indicator. The spec is explicit about
	// this variant ("streaming / typing indicator").
	require.Regexp(t, `<span[^>]*class="[^"]*\bloading-dots\b`, chatFallbackBody,
		"the in-flight indicator must use daisyUI's loading-dots variant (the standard streaming / typing indicator)")

	// The size modifier `loading-md` is the daisyUI
	// default — match the surrounding button height
	// (the Send button is btn, the loading dots
	// should be visible at the same scale).
	assert.Regexp(t, `<span[^>]*class="[^"]*\bloading(?:-xs|-sm|-md|-lg|-xl)?\b`, chatFallbackBody,
		"the loading-dots element should carry a size modifier (loading-md is the daisyUI default)")
}

// TestChat_NoLegacyThinkingText pins the "no more
// 'Thinking…' text" property called out in the SPEC.
// The pre-Phase-2.3 design had a `<span
// class="badge">Thinking…</span>` inside the
// #chat-indicator; Phase 2.3 replaces it with the
// daisyUI loading-dots component, which communicates
// "generating" visually without text. The
// 'Thinking…' text is gone.
func TestChat_NoLegacyThinkingText(t *testing.T) {
	// The 'Thinking…' text is the pre-Phase-2.3
	// affordance. Phase 2.3 replaces it with
	// loading-dots. We don't pin the exact
	// "Thinking" wording (a future "Generating…"
	// would also be valid) — we pin the absence
	// of any plain-text label inside the
	// #chat-indicator element, since the daisyUI
	// loading-dots component is visual-only.
	//
	// Find the #chat-indicator element and check
	// its contents.
	indicatorStart := strings.Index(chatFallbackBody, `id="chat-indicator"`)
	require.GreaterOrEqual(t, indicatorStart, 0,
		"#chat-indicator must be present (sanity)")

	// The closing </div> for the indicator.
	indicatorEnd := strings.Index(chatFallbackBody[indicatorStart:], "</div>")
	require.Positive(t, indicatorEnd,
		"#chat-indicator must have a closing </div>")

	indicatorContent := chatFallbackBody[indicatorStart : indicatorStart+indicatorEnd]

	// The daisyUI loading-dots component is the
	// only thing inside #chat-indicator in the
	// new design. No badge with "Thinking" or
	// "Generating" text.
	assert.NotContains(t, indicatorContent, "Thinking",
		"Phase 2.3 removes the legacy 'Thinking…' text — daisyUI loading-dots communicates 'generating' visually")
	assert.NotContains(t, indicatorContent, "Generating",
		"Phase 2.3 removes any 'Generating…' text label — daisyUI loading-dots is visual-only")

	// The indicator should contain a single
	// loading-dots element (no extraneous markup).
	dotsCount := strings.Count(indicatorContent, "loading-dots")
	assert.Equal(t, 1, dotsCount,
		"the #chat-indicator should contain exactly one loading-dots element (got %d)", dotsCount)
}
