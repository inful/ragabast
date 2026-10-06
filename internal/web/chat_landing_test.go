package web

import (
	"regexp"
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

// TestChatLog_HasMaxWidthForLegibleLayout was removed. The
// v0.16.5 release added max-width: 56rem + margin-inline: auto
// to .chat-log and #chat-form to give the chat a "comfortable
// panel" feel on wide viewports, but the user subsequently
// reported that the chat should match documents/search and use
// the full container width. The replacement tests below pin
// the actual contract.
//
// See TestChatLog_UsesFullBodyWidth and
// TestChatBody_NoStickyNavbarGap for the new pins.

// TestChatBody_HasHorizontalPadding pins the "chat page
// has breathing room from the viewport edges" contract.
// The body class drops the daisyUI `container` class
// (so the chat cards can use the full viewport width)
// and chat.css supplies the horizontal padding in its
// place — 8px on small viewports, 16px from 640px up.
// Without this rule the chat-log card and chat-form
// card would touch the viewport edges on a full-bleed
// layout, which is a design regression.
//
// Why the padding lives in chat.css (not as a `px-2
// sm:px-4` utility on the body): the shipped
// daisyui.min.css is tree-shaken from the @source
// files. A body-class change would need a `make css`
// rebuild to land in the embedded bundle. chat.css
// ships verbatim from the source file (no tree-
// shaking), so a CSS-only change here takes effect
// without a rebuild.
func TestChatBody_HasHorizontalPadding(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	// The body element must declare padding-inline.
	// We allow any value — the comment in chat.css
	// documents the responsive scale (0.5rem on
	// small, 1rem from 40rem up).
	assert.Regexp(t, `body\s*\{[^}]*padding-inline:`, css,
		"chat.css must declare padding-inline on body so the chat cards have breathing room from the viewport edges (the body class dropped the daisyUI `container` class so the cards can use the full viewport width; the padding has to live somewhere)")

	// The responsive step (>= 640px / 40rem) must
	// increase the padding so the chat doesn't
	// look cramped on tablets/desktops.
	assert.Regexp(t, `@media\s*\(\s*min-width:\s*40rem\s*\)\s*\{[^}]*body\s*\{[^}]*padding-inline:`, css,
		"chat.css must declare a larger padding-inline on body at the sm breakpoint (40rem) so the breathing room scales up on tablets/desktops")
}

// TestChatLog_UsesFullBodyWidth pins the "chat cards fill
// the available body width" contract. The user has
// reported the chat feels narrow three times now:
//  1. v0.16.5 56rem cap on the cards (fixed by
//     removing the cap and letting the cards fill the
//     body container).
//  2. daisyUI `container` class on the body (fixed by
//     removing the class so the body fills the viewport).
//  3. The chat bubbles themselves still looked narrow
//     after the card fix — the root cause was the
//     `prose chat-turn` wrapper carrying the typography
//     plugin's 65ch cap, which constrains the entire
//     chat grid inside it. Fixed by `.prose.chat-turn
//     { max-width: none }` in chat.css; the assistant
//     reply's own prose cap is widened to 90ch on
//     viewports >= 48rem.
//
// The current contract: the body, the chat-log card, and
// the chat-form card each span the full viewport width
// (with a small breathing-room padding on the body). The
// chat-turn wrapper fills the card width; the daisyUI
// chat grid inside it lays out the operator and assistant
// bubbles in their respective columns; the operator
// bubble is capped at 90% of its grid column (daisyUI's
// default — see TestShippedCSS_OperatorBubbleMaxWidth)
// and the assistant reply's prose text is capped at 90ch
// on desktop as a readability guardrail (see the
// .chat-msg.prose rule in chat.css).
//
// The test pins both sides of the contract:
//   - chat.css has no max-width cap on .chat-log or
//     #chat-form (the CSS half).
//   - The rendered <body> on the chat page does NOT
//     carry the daisyUI `container` class (the
//     template half — the body must not re-introduce
//     a max-width constraint).
func TestChatLog_UsesFullBodyWidth(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	// .chat-log must not have a max-width cap. The
	// chat-turn wrapper inside has its own cap override
	// (.prose.chat-turn { max-width: none } — see
	// TestShippedCSS_ChatTurnResetsProseMaxWidth), and
	// the per-bubble caps (operator 90%, assistant 90ch
	// on desktop) keep individual messages readable.
	assert.NotRegexp(t, `\.chat-log\s*\{[^}]*max-width:`, css,
		".chat-log must not have a max-width cap — the chat card should fill the body width, matching documents/search and using every available horizontal pixel on wide viewports")

	// Same for #chat-form. The textarea and source-kind
	// checkboxes line up with the chat scrollback above
	// because both fill the body width.
	assert.NotRegexp(t, `#chat-form\s*\{[^}]*max-width:`, css,
		"#chat-form must not have a max-width cap — the input form should fill the body width, matching the chat-log above it")

	// Same for the centering — no margin-inline: auto on
	// the chat-log card now that it fills the body.
	assert.NotRegexp(t, `\.chat-log\s*\{[^}]*margin-inline:\s*auto`, css,
		".chat-log must not use margin-inline: auto to center a narrower card — the card now fills the body")

	// Now the template half: the chat fallback's
	// <body> must not carry the daisyUI `container`
	// class. `container` is a centered max-width utility
	// that caps the page at the daisyUI 2xl breakpoint
	// (~1536px) on wide viewports — exactly the
	// "chat feels narrow on ultrawide displays" symptom
	// the user reported. Removing it lets the body
	// fill the viewport; the chat-log card fills the
	// body; per-bubble max-widths keep messages
	// readable.
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

	bodyClass := bodyRE(body, `<body\s+class="([^"]*)"`)
	require.NotEmpty(t, bodyClass, "chat fallback must render a <body> with a class attribute")
	assert.NotRegexp(t, `\bcontainer\b`, bodyClass,
		"the chat <body> class must not include daisyUI's `container` — the centered max-width utility caps the page at ~1536px on wide viewports, leaving the chat feeling narrow on ultrawide displays. The per-bubble max-widths (operator 70%, assistant 65ch) handle readability; the card itself should fill the body.")
}

// TestChatBody_NoStickyNavbarGap pins the "no gap between the
// navbar and the chat card" contract. The chat body used to
// carry `pt-16` (4rem padding-top), which created a 64px gap
// between the sticky navbar and the chat-log card on every
// page load. Documents and search don't carry that class and
// flow naturally below the navbar — the chat should match.
//
// `pt-16` was originally added in Phase 2.4 of
// plans/ux-overhaul.md (sticky navbar) to keep the navbar
// from overlapping the first message. With the navbar's
// actual measured height of 64px and a margin-top on the
// body (mt-4 = 16px), 4rem of padding over-corrects the
// issue and creates the visible gap. The simpler fix is to
// just not have pt-16 — the navbar's natural position is
// right above the content, and the existing mt-4 supplies a
// small breathing-room gap.
func TestChatBody_NoStickyNavbarGap(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
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

	// The chat body class must NOT include pt-16.
	bodyClass := bodyRE(body, `<body\s+class="([^"]*)"`)
	require.NotEmpty(t, bodyClass, "chat fallback must render a <body> with a class attribute")
	assert.NotContains(t, bodyClass, "pt-16",
		"the chat body class must not include pt-16 — that padding-top creates a 64px gap between the sticky navbar and the chat card that documents/search don't have")
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

// bodyRE extracts the class attribute of the first <body>
// element in the rendered HTML. Used by tests that need to
// pin a single class token (e.g. "the chat body must not
// include pt-16") without coupling to the rest of the class
// string. Returns the captured group on match, "" on no match.
func bodyRE(body, pattern string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
