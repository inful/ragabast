package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// suggestedPromptRE is compiled once at package init so
// the test's hot loop doesn't re-compile on every
// invocation. The pattern matches the data attribute
// value (capture group 1).
var suggestedPromptRE = regexp.MustCompile(`data-suggested-prompt="([^"]*)"`)

// TestChat_EmptyStateHasSuggestedPrompts pins the Phase 2.1
// contract from plans/ux-overhaul.md: the chat landing
// page's empty state replaces the plain orienting sentence
// with clickable suggested prompts. New users do not
// know what to ask; the prompts turn "type something" into
// a discoverable choice.
//
// Contract:
//   - The placeholder is the existing
//     #chat-messages-placeholder element (the fragment that
//     htmx swaps out when the first message arrives).
//   - At least 2 clickable <button> elements with
//     data-suggested-prompt="..." render inside the
//     placeholder.
//   - The buttons use daisyUI btn btn-ghost btn-block
//     classes (the standard "selectable row" pattern).
//
// We pin the data-suggested-prompt attribute (the JS
// hook) rather than the button text, so a future
// contributor can change the copy without breaking the
// test. We pin "at least 2" rather than an exact count
// so the designer can add / drop prompts.
func TestChat_EmptyStateHasSuggestedPrompts(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	data := chatFallbackData{
		Title:     "Chat",
		CsrfToken: "",
		SessionID: "session-test",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
		},
	}
	var buf strings.Builder
	require.NoError(t, s.fallback.chat.Execute(&buf, data))
	body := buf.String()

	// Sanity: the placeholder is still where the
	// swap-out fragment expects it. The Phase 2.1
	// change rewrites the children, not the
	// container.
	require.Contains(t, body, `id="chat-messages-placeholder"`,
		"#chat-messages-placeholder must remain — the chat_message.html fragment swaps it out via hx-swap-oob")

	// At least 2 clickable suggested prompts.
	// The regex captures the data attribute value
	// in capture group 1; we count the matches
	// rather than the exact copy (the copy is a
	// design call — the structure is the contract).
	matches := suggestedPromptRE.FindAllStringSubmatch(body, -1)
	require.GreaterOrEqual(t, len(matches), 2,
		"the chat empty state must render at least 2 clickable suggested-prompt buttons (got %d)", len(matches))

	// The prompts must be <button> elements
	// (clickable, not <a> links that would change
	// the URL). Pin that the data-suggested-prompt
	// attribute sits on a <button> tag.
	assert.Regexp(t, `<button[^>]*data-suggested-prompt=`, body,
		"the suggested-prompt elements must be <button>s (clickable in place, not <a> navigation)")

	// The buttons must use daisyUI's btn + btn-ghost +
	// btn-block so they pick up the standard selectable
	// row styling. The btn-ghost keeps them quiet
	// (not bright primary CTAs that compete with the
	// Send button); btn-block makes them full-width
	// in the centered card.
	//
	// The regex matches a <button data-suggested-prompt...>
	// element whose class attribute carries the named
	// token. Class attribute order is not pinned.
	btnClassRE := `<button[^>]*class="[^"]*\bbtn\b[^"]*"[^>]*data-suggested-prompt=`
	require.Regexp(t, btnClassRE, body,
		"the suggested-prompt buttons must use daisyUI's btn class so they pick up the standard button styling")
	ghostClassRE := `<button[^>]*class="[^"]*\bbtn-ghost\b[^"]*"[^>]*data-suggested-prompt=`
	require.Regexp(t, ghostClassRE, body,
		"the suggested-prompt buttons must use daisyUI's btn-ghost so they read as quiet selectors, not bright CTAs that compete with Send")
	blockClassRE := `<button[^>]*class="[^"]*\bbtn-block\b[^"]*"[^>]*data-suggested-prompt=`
	require.Regexp(t, blockClassRE, body,
		"the suggested-prompt buttons must use daisyUI's btn-block so they render as full-width rows in the centered card")
}

// TestChat_SuggestedPromptClickHandler pins the Phase 2.1
// chat.js surface: a delegated click handler on the
// #chat-messages-placeholder element reads the clicked
// button's data-suggested-prompt attribute, copies the
// value into the chat textarea, and focuses the textarea
// so the operator can edit (or just press Enter) without
// re-targeting the input.
//
// The handler is the JS half of the contract. Without it,
// the placeholder buttons would be markup-only — the
// click would do nothing, and a designer who trusted the
// visual would discover the gap the first time they
// tried to use a suggested prompt.
func TestChat_SuggestedPromptClickHandler(t *testing.T) {
	body := readStaticAsset(t, "/static/chat.js")

	// The handler must delegate from the placeholder
	// (not per-button) so the wiring survives the
	// placeholder being removed after the first click.
	// Phase 2.1 ships the remove-on-click behavior;
	// Phase 2.5 (auto-scroll) may add a richer flow.
	require.Contains(t, body, "chat-messages-placeholder",
		"chat.js must install a click handler on #chat-messages-placeholder (Phase 2.1 of plans/ux-overhaul.md)")

	// The handler must read the data-suggested-prompt
	// attribute. The daisyUI HTML standard is to
	// kebab-case the attribute; JS dataset converts
	// it to camelCase (suggestedPrompt). Pin the
	// dataset access so a future contributor who
	// switches to getAttribute() is a deliberate
	// change.
	require.Contains(t, body, "suggestedPrompt",
		"the click handler must read dataset.suggestedPrompt (Phase 2.1 of plans/ux-overhaul.md)")

	// The handler must focus the textarea so the
	// operator can edit the prompt or just press
	// Enter to send. focus() is the browser API;
	// the spec pins that the handler is opinionated
	// about the user not having to click again.
	require.Contains(t, body, "input.focus",
		"the click handler must focus the chat input so the operator can edit or send immediately")
}
