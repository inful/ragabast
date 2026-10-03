package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// chatStickyNavbarTestServer builds the standard
// chat-test Server fixture for the Phase 2.4 tests.
func chatStickyNavbarTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil
	return s
}

// renderChatStickyNavbarFallback executes the chat
// landing fallback template and returns the HTML.
func renderChatStickyNavbarFallback(t *testing.T, s *Server) string {
	t.Helper()
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
	return buf.String()
}

// TestChat_NavbarIsSticky pins the Phase 2.4 contract from
// plans/ux-overhaul.md: the chat landing page's navbar is
// sticky to the top of the viewport so long chat sessions
// keep navigation accessible. Without sticky positioning,
// the navbar scrolls away with the page and the user has
// to scroll back up to navigate to Search or Documents.
//
// daisyUI's `navbar` component works as a sticky element
// with two additions: `sticky top-0` (the Tailwind
// positioning utility) and `z-10` (so the navbar layers
// above any in-page content). The body needs a
// `pt-16` to compensate for the navbar height (the
// sticky bar would otherwise cover the top of the
// page content).
func TestChat_NavbarIsSticky(t *testing.T) {
	body := renderChatStickyNavbarFallback(t, chatStickyNavbarTestServer(t))

	// The <nav> element must carry sticky + top-0.
	// daisyUI's navbar class + Tailwind's sticky /
	// top-0 utilities compose into a sticky
	// navigation bar.
	navStart := strings.Index(body, "<nav ")
	require.GreaterOrEqual(t, navStart, 0, "the page must render a <nav> element (the navbar)")

	// Find the closing > of the nav's opening tag.
	navEnd := strings.Index(body[navStart:], ">")
	require.Positive(t, navEnd, "the <nav> opening tag must have a closing >")
	navTag := body[navStart : navStart+navEnd+1]

	assert.Contains(t, navTag, "navbar",
		"the <nav> element must use daisyUI's navbar class")
	assert.Regexp(t, `\bsticky\b`, navTag,
		"the navbar must use Tailwind's sticky utility (Phase 2.4 of plans/ux-overhaul.md)")
	assert.Regexp(t, `\btop-0\b`, navTag,
		"the sticky navbar must be pinned to the top with top-0")
	assert.Regexp(t, `\bz-\d+\b`, navTag,
		"the sticky navbar must declare a z-* utility so it layers above page content")
}

// TestChat_BodyHasNavbarHeightCompensation pins the second
// half of the sticky-navbar contract: the body needs
// `pt-16` (or equivalent) so the page content doesn't
// disappear under the sticky navbar. Tailwind's `pt-16`
// is 4rem (16 * 0.25rem), which matches daisyUI's
// default navbar min-height of 4rem.
func TestChat_BodyHasNavbarHeightCompensation(t *testing.T) {
	body := renderChatStickyNavbarFallback(t, chatStickyNavbarTestServer(t))

	bodyStart := strings.Index(body, "<body")
	require.GreaterOrEqual(t, bodyStart, 0, "the page must render a <body> element")
	bodyEnd := strings.Index(body[bodyStart:], ">")
	require.Positive(t, bodyEnd, "the <body> opening tag must have a closing >")
	bodyTag := body[bodyStart : bodyStart+bodyEnd+1]

	assert.Regexp(t, `\bpt-(\d+)\b`, bodyTag,
		"the <body> element must declare a pt-* top-padding utility so the sticky navbar doesn't cover the page content (Phase 2.4 of plans/ux-overhaul.md)")
}
