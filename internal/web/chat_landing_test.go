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

// TestChatLog_HasMaxWidthForLegibleLayout pins the
// "chat interface isn't stretched across the full container"
// contract. Without an explicit max-width, the chat-log card
// and the chat-form card both inherit the body container's
// width (up to 96rem / 1536px on extra-large viewports). The
// bubbles inside are constrained by `.prose { max-width: 65ch
// }` to about 65 characters (~520px), which leaves a sea of
// empty card around them on wide screens and makes the chat
// feel sparse and unfocused.
//
// The fix in chat.css caps the chat cards at a narrower
// "comfortable chat panel" width — the same range ChatGPT and
// Notion AI use (~48-56rem). margin-inline: auto centers the
// narrower card within the container.
//
// Regression guard for the user-reported "chat interface has
// become somewhat cramped and unreadable" issue (the
// underlying cause was cards that were too WIDE, leaving the
// bubbles isolated in empty space rather than filling the
// chat panel).
func TestChatLog_HasMaxWidthForLegibleLayout(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	// The chat-log card must declare a max-width so the
	// chat interface isn't stretched to the full container
	// width on wide viewports. The exact value is a design
	// call (we don't pin the number — a future contributor
	// may want to use 48rem or 64rem). What matters is that
	// some max-width is declared.
	assert.Regexp(t, `\.chat-log\s*\{[^}]*max-width:`, css,
		".chat-log must declare a max-width so the chat panel is not stretched to the full container width on wide viewports (ChatGPT / Notion AI use ~48-56rem)")

	// The chat-form card must also be constrained, so the
	// textarea and source-kind checkboxes line up visually
	// with the chat-log above them. Without this, the form
	// spans the full container and the input feels detached
	// from the chat scrollback it's feeding.
	assert.Regexp(t, `#chat-form\s*\{[^}]*max-width:`, css,
		"#chat-form must declare a max-width so the input form does not stretch wider than the chat-log card above it")

	// The narrowing must be balanced by margin-inline: auto
	// so the narrower card is centered inside the wider
	// container, not pinned to the start edge.
	assert.Regexp(t, `\.chat-log\s*\{[^}]*margin-inline:\s*auto`, css,
		".chat-log must declare margin-inline: auto so the narrower chat panel is centered within the container, not pinned to the start edge")
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

// TestChatLanding_PageHeaderInsideChatLogBody pins the
// "the page header (h1 + subtitle) and the chat scrollback
// share the same column" contract. Before this fix the
// h1 and subtitle were body-level children with the chat
// card centered to a 56rem max-width; the visual gap
// between the body-level header (x=0) and the centered
// card (x=192 on a 1280px viewport) made the page header
// look detached from the scrollback below it. The h1
// and subtitle now live INSIDE the chat-log card-body so
// they align vertically with the messages.
//
// The test runs the chat fallback end-to-end (rather than
// reading the static chatFallbackBody source) so a future
// parsing regression — e.g. a re-introduced
// {{ template "header" }} inside an HTML comment — trips
// the test rather than silently regressing. Static-source
// regression guards live separately in
// internal/web/login_template_comment_test.go.
func TestChatLanding_PageHeaderInsideChatLogBody(t *testing.T) {
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

	// The chat-log card-body must contain BOTH the h1
	// and the subtitle, before the chat-messages-placeholder.
	// We pin the structural ordering (h1, subtitle,
	// placeholder) inside the card, not the exact markup,
	// so a future contributor can swap <p> for <div> or
	// wrap things in <header> without breaking the test.
	cardOpenIdx := strings.Index(body, `id="chat-messages"`)
	cardBodyOpenIdx := strings.Index(body[cardOpenIdx:], "card-body")
	h1Idx := strings.Index(body, "<h1")
	subtitleIdx := strings.Index(body, "Retrieval-augmented chat")
	placeholderIdx := strings.Index(body, `id="chat-messages-placeholder"`)
	require.Positive(t, cardOpenIdx, "chat-messages card must render")
	require.Positive(t, cardBodyOpenIdx, "card-body wrapper must render")
	require.Positive(t, h1Idx, "h1 must render")
	require.Positive(t, subtitleIdx, "subtitle must render")
	require.Positive(t, placeholderIdx, "placeholder must render")
	require.Greater(t, h1Idx, cardOpenIdx+cardBodyOpenIdx,
		"the h1 must live inside the chat-messages card-body, not as a body-level child above the card")
	require.Less(t, subtitleIdx, placeholderIdx,
		"the subtitle must appear before the placeholder so the title block still introduces the empty state")
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
