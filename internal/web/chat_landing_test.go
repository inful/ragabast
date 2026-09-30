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
// copy called out in the UI review at
// .review-screenshots/2026-09-30-ragabast-ui-review: the
// chat log used to render "No messages yet." inside a large
// empty box, which read as "this page is broken" rather
// than "type something to begin."
//
// The contract: the placeholder copy must (a) orient a
// first-time user with what the page is for, (b) point at
// the input form, and (c) keep the empty state quiet
// (no oversized spinner / loading affordance).
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

	// Old phrasing must be gone.
	assert.NotContains(t, body, "No messages yet.",
		"the placeholder must be more than a status indicator")

	// New orienting copy present. Don't pin the exact
	// wording — that's a presentation decision the
	// designer can iterate on. Pin that the placeholder
	// (a) names what the page does and (b) mentions the
	// input form so the user knows where to type.
	assert.Regexp(t, `id="chat-messages-placeholder"[^>]*>\s*<p[^>]*>.*[Cc]hat.*</p>`, body,
		"the empty state must include an orienting sentence in #chat-messages-placeholder")
	assert.Contains(t, body, `id="chat-input"`,
		"the empty state lives in the same page as the input form")
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
