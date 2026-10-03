package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestChatLog_HasRelativePositioning pins the fix for the
// "Jump to latest" button anchoring: the .jump-to-latest
// element is `position: absolute` (it pins to the bottom-
// right of its container), so the container itself must
// declare `position: relative`. Without that, the button
// anchors to the initial containing block and floats at
// the bottom-right of the viewport, away from the chat log
// it belongs to.
//
// Pinning this here means a future contributor who refactors
// chat.css and drops the `position: relative` rule gets a
// red test, with a comment that explains why it matters.
func TestChatLog_HasRelativePositioning(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	// The .chat-log block must declare position: relative.
	// Match the comment-led block: ".chat-log { ... }" with
	// "position: relative" inside.
	assert.Regexp(t, `\.chat-log\s*\{[^}]*position:\s*relative`, css,
		".chat-log must declare position: relative so the .jump-to-latest button pins to the container, not the viewport")
}

// TestChatLanding_OrientingPlaceholder pins the empty-state
// design called out in the UI review at
// .review-screenshots/2026-09-30-ragabast-ui-review: the
// chat log used to render "No messages yet." inside a large
// empty box, which read as "this page is broken" rather
// than "type something to begin."
//
// Phase 2.1 of plans/ux-overhaul.md replaces the plain
// orienting sentence with a clickable list of suggested
// prompts. The contract is now: the placeholder is a
// centered card (icon + heading + button list) — the
// structural shape is the contract, the exact copy is a
// design call. TestChat_EmptyStateHasSuggestedPrompts
// pins the data-suggested-prompt buttons specifically;
// this test pins the wider "centered card" structure.
func TestChatLanding_OrientingPlaceholder(t *testing.T) {
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

	// Old phrasing must be gone — "No messages yet."
	// was the original "this page is broken" copy.
	assert.NotContains(t, body, "No messages yet.",
		"the placeholder must be more than a status indicator")

	// The placeholder must use the new centered-card
	// structure: flex flex-col items-center text-center
	// on the prose wrapper turns the placeholder into a
	// vertically stacked, horizontally centered block
	// rather than the old left-aligned prose paragraph.
	// Pin the daisyUI flex utility rather than the
	// exact heading copy so the designer can iterate
	// on "What would you like to know?" without
	// breaking the test.
	//
	// The regex matches the placeholder tag with
	// class and id in either order (HTML5 allows
	// either) — the placeholder is found by id, and
	// its class attribute is checked for the
	// structural tokens.
	placeholderRE := `<div[^>]*\bid="chat-messages-placeholder"[^>]*>` +
		`|<div[^>]*class="[^"]*\bprose\b[^"]*\bflex\b[^"]*\bflex-col\b[^"]*"[^>]*>`
	assert.Regexp(t, placeholderRE, body,
		"#chat-messages-placeholder must use a flex flex-col layout (Phase 2.1 centered-card empty state)")

	// The empty state lives in the same page as the
	// input form (it points the user at the form).
	// Pin the input's id to keep the cross-reference
	// honest: a future refactor that splits the empty
	// state onto its own page would lose the
	// "what to do next" affordance.
	assert.Contains(t, body, `id="chat-input"`,
		"the empty state lives in the same page as the input form")
}

// TestChatLanding_SendButtonUsesPrimaryAction pins the
// color-grammar contract: the chat landing page's primary
// call-to-action must use daisyUI's `btn-primary` color
// modifier, not `btn-warning`.
//
// Why: yellow is reserved in the daisyUI / Bootstrap /
// Material idioms for *caution* signals ("your changes are
// unsaved", "this API key is about to be revoked"). Using
// it for the Send button drowns every future warning
// surface in the same UI, and a first-time operator's eye
// reads the yellow as a hazard, not a CTA.
//
// Phase 2f of the Bulma -> DaisyUI migration (see
// plans/daisyui-migration.md) flipped the class from
// `button is-primary` to `btn btn-primary`. The test
// name and intent stay the same; the sentinel is
// updated.
func TestChatLanding_SendButtonUsesPrimaryAction(t *testing.T) {
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

	assert.Contains(t, body, `id="chat-send" class="btn btn-primary"`,
		"the Send button must render with class=\"btn btn-primary\" — yellow (btn-warning) reads as a hazard, not a CTA")
	assert.NotContains(t, body, `id="chat-send" class="btn btn-warning"`,
		"the Send button must NOT render with class=\"btn btn-warning\" — yellow is reserved for caution surfaces")
}

// TestChatLanding_AriaLiveOnMessageLog pins the
// accessibility contract: new assistant replies must be
// announced to screen readers via an aria-live region.
//
// htmx's `hx-swap=\"beforeend\"` appends reply fragments to
// #chat-messages. Without aria-live, a screen reader user
// who submitted a question would not be told when the
// answer arrived. The chat-messages container is the
// natural place for the live region because all assistant
// replies land inside it.
func TestChatLanding_AriaLiveOnMessageLog(t *testing.T) {
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

	assert.Contains(t, body, `id="chat-messages"`,
		"#chat-messages must render (sanity)")
	// The aria-live attribute must be on the container,
	// not on a child, so announcements cover every
	// append. We assert the literal attribute pair on
	// the same element via a regex that matches the
	// opening tag.
	assert.Regexp(t, `<div[^>]*\bid="chat-messages"[^>]*aria-live="polite"`, body,
		"#chat-messages must declare aria-live=\"polite\" so screen readers announce new replies")
}
