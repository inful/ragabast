package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// TestChatMessageFragment_UsesChatBubbleLayout pins the
// Phase 2.2 contract from plans/ux-overhaul.md: the chat
// message fragment uses daisyUI's purpose-built chat
// component (chat-start / chat-end, chat-bubble, chat-image,
// chat-header, chat-footer) instead of the generic card
// surface that the Bulma-era fragment used.
//
// The component is what makes the chat feel like a chat:
// the avatar sits on the appropriate side, the header
// carries name + timestamp, the bubble is the message
// surface, the footer is the action bar. Compare to the
// pre-Phase-2.2 design where every message was a flat
// card with no avatar, no timestamp, no action bar.
func TestChatMessageFragment_UsesChatBubbleLayout(t *testing.T) {
	body := fetchChatMessageBody(t, "" /* no sources */)

	// The operator message is on the start side
	// (left in LTR). daisyUI's chat-start is the
	// placement class.
	require.Regexp(t, `class="[^"]*\bchat\b[^"]*\bchat-start\b`, body,
		"the operator message must use daisyUI's chat-start placement class (Phase 2.2 of plans/ux-overhaul.md)")

	// The assistant message is on the end side
	// (right in LTR).
	require.Regexp(t, `class="[^"]*\bchat\b[^"]*\bchat-end\b`, body,
		"the assistant message must use daisyUI's chat-end placement class")

	// The chat-bubble is the message surface. The
	// operator uses chat-bubble-primary (the daisyUI
	// primary color), the assistant uses the default
	// (neutral).
	assert.Regexp(t, `chat-bubble[^"]*chat-bubble-primary`, body,
		"the operator chat-bubble must use daisyUI's chat-bubble-primary color modifier")
}

// TestChatMessageFragment_HasAvatarAndTimestamp pins the
// "name + timestamp" affordance called out in the SPEC.
// The chat-header is the daisyUI component that carries
// this metadata; an avatar sits in chat-image next to it.
func TestChatMessageFragment_HasAvatarAndTimestamp(t *testing.T) {
	body := fetchChatMessageBody(t, "")

	// The chat-image element is the daisyUI avatar
	// anchor. The component name (chat-image) is
	// what the SPEC uses; the inner daisyUI
	// `avatar` class styles the actual circle.
	assert.Regexp(t, `class="[^"]*\bchat-image\b[^"]*"`, body,
		"the chat message fragment must use daisyUI's chat-image element (avatar anchor)")

	// The chat-header is the metadata line
	// (name + timestamp).
	assert.Regexp(t, `class="[^"]*\bchat-header\b`, body,
		"the chat message fragment must use daisyUI's chat-header for the name + timestamp line")

	// A <time> element carries the timestamp in a
	// machine-readable format. The daisyUI docs
	// pattern uses datetime="..." for ISO 8601.
	assert.Regexp(t, `<time[^>]*datetime=`, body,
		"the chat message fragment must include a <time> element with a datetime attribute (the daisyUI pattern)")

	// The "You" / "Assistant" labels appear in the
	// chat-header. We don't pin the exact wording
	// (the design can change to "Operator" /
	// "Model"), but the labels must be present.
	assert.Regexp(t, `\bYou\b`, body,
		"the chat message fragment must label the operator as 'You' in the chat-header")
	assert.Regexp(t, `\bAssistant\b`, body,
		"the chat message fragment must label the assistant as 'Assistant' in the chat-header")
}

// TestChatMessageFragment_ActionBarOnAssistantBubble pins
// the "action bar (copy, regenerate, sources badge) on
// hover" affordance. The chat-footer element is the
// daisyUI container; the buttons inside carry the
// data-action attribute that chat.js wires up.
//
// The spec mentions a "thumbs up / down" button in the
// prose, but the Phase 2.2 implementation only includes
// copy + regenerate (thumbs would require server-side
// feedback storage which is out of scope).
func TestChatMessageFragment_ActionBarOnAssistantBubble(t *testing.T) {
	body := fetchChatMessageBody(t, "")

	// The chat-footer carries the action bar.
	assert.Regexp(t, `class="[^"]*\bchat-footer\b`, body,
		"the assistant message must use daisyUI's chat-footer element for the action bar")

	// The action bar must be on the assistant
	// bubble, not the operator bubble. daisyUI's
	// chat component is symmetrical; the placement
	// is structural (which chat element wraps the
	// footer), not visual. Pin that the chat-footer
	// sits inside the chat-end (assistant) block.
	chatEndFooter := betweenChatEndAndChatDivider(body)
	require.NotEmpty(t, chatEndFooter, "the chat-footer must render inside the assistant (chat-end) message block")
	assert.Contains(t, chatEndFooter, "chat-footer",
		"the chat-footer must be inside the assistant (chat-end) message block, not the operator (chat-start) block")

	// data-action="copy" and data-action="regenerate"
	// are the two buttons the spec lists. chat.js
	// wires them; the markup just declares the
	// hooks.
	assert.Regexp(t, `data-action="copy"`, body,
		"the action bar must render a button with data-action=\"copy\" (Phase 2.2 of plans/ux-overhaul.md)")
	assert.Regexp(t, `data-action="regenerate"`, body,
		"the action bar must render a button with data-action=\"regenerate\"")

	// The action bar uses daisyUI btn + btn-ghost +
	// btn-xs so the buttons read as quiet
	// affordances, not bright CTAs. The xs size
	// keeps the row compact.
	assert.Regexp(t, `<button[^>]*class="[^"]*\bbtn\b[^"]*"[^>]*data-action="copy"`, body,
		"the copy button must use daisyUI's btn class")
	assert.Regexp(t, `<button[^>]*class="[^"]*\bbtn-ghost\b[^"]*"[^>]*data-action="copy"`, body,
		"the copy button must use btn-ghost so it reads as a quiet affordance, not a primary CTA")
	assert.Regexp(t, `<button[^>]*class="[^"]*\bbtn-xs\b[^"]*"[^>]*data-action="copy"`, body,
		"the copy button must use btn-xs so the action bar stays compact")
}

// TestChatMessageFragment_SourcesAsBadges pins the
// "Citation badges inline instead of a collapsed <details>"
// affordance. Sources become flex-wrapped badge chips,
// each linking to the source's citation URL.
//
// The daisyUI pattern (from the SPEC):
// <a href="..." class="badge badge-info badge-sm hover:badge-primary"
//
//	data-tooltip="...">{{ .DisplayLabel }}</a>
func TestChatMessageFragment_SourcesAsBadges(t *testing.T) {
	// Synthesize a fake service that returns two
	// sources so the test exercises the badge path
	// rather than the no-sources path.
	body := fetchChatMessageBody(t, "https://example.com/doc1,https://example.com/doc2")

	// No more collapsed <details> for sources. The
	// pre-Phase-2.2 design wrapped the sources in
	// <details><summary>...</summary>... so
	// "synthesis data was easy to miss" (per the
	// SPEC).
	assert.NotContains(t, body, "<details",
		"the chat message fragment must not use <details> for sources (Phase 2.2 replaces with inline badges)")

	// Sources render as <a> badges, not <li> rows
	// in a <ol>. Pin the badge class on the
	// anchor.
	assert.Regexp(t, `<a[^>]*class="[^"]*\bbadge\b[^"]*\bbadge-info\b`, body,
		"each source must render as a daisyUI badge-info anchor (Phase 2.2 inline citation pattern)")
	assert.Regexp(t, `<a[^>]*class="[^"]*\bbadge\b[^"]*\bbadge-sm\b`, body,
		"each source badge must use badge-sm so the row of citations stays compact")

	// The "N sources" badge in the action bar uses
	// data-tooltip (daisyUI's tooltip component
	// pattern) for the hover detail. Pin the
	// attribute so a refactor to a different
	// tooltip mechanism is a deliberate choice.
	assert.Regexp(t, `data-tooltip=`, body,
		"the sources summary badge must carry a data-tooltip attribute (daisyUI tooltip pattern)")
}

// fetchChatMessageBody POSTs to /chat/message and returns
// the response body. The optional sourcesCSV parameter
// is a comma-separated list of citation URLs to seed the
// fake service's reply; pass "" for a sources-free reply.
func fetchChatMessageBody(t *testing.T, sourcesCSV string) string {
	t.Helper()

	cfg := config.DefaultConfig()
	svc := &fakeService{
		queryAnswer: "Here is what I found.",
	}
	if sourcesCSV != "" {
		// Seed the service with stub sources so the
		// badge-rendering path is exercised. Each
		// URL becomes a SearchResult with a single
		// CitationURL (the primary "direct link" the
		// badge will surface).
		urls := splitSourcesCSV(sourcesCSV)
		results := make([]models.SearchResult, 0, len(urls))
		for i, u := range urls {
			results = append(results, models.SearchResult{
				DocumentTitle: "Source " + string(rune('1'+i)),
				DocumentID:    "doc-" + string(rune('1'+i)),
				SourceKind:    models.SourceDocbuilder,
				CitationURL:   u,
			})
		}
		svc.queryDebug = &service.QueryDebugInfo{Results: results}
	}
	s := NewServer(cfg, svc)

	form := "message=what+does+adr-001+say"
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/chat/message",
		strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.String()
}

// betweenChatEndAndChatDivider extracts the text between
// the opening of the chat-end (assistant) message block
// and the start of the chat-turn-divider. Used to assert
// that the chat-footer lives inside the assistant block
// (not the operator block).
//
// The "end of the assistant block" is the chat-turn-divider
// <hr> that follows it. The pre-Phase-2.2 layout had the
// divider OUTSIDE the assistant card; the new layout
// keeps that pattern (the divider is the bottom marker).
func betweenChatEndAndChatDivider(body string) string {
	// Find the chat-end opener.
	endIdx := strings.Index(body, "chat-end")
	if endIdx < 0 {
		return ""
	}
	// Find the chat-turn-divider after that point.
	dividerIdx := strings.Index(body[endIdx:], "chat-turn-divider")
	if dividerIdx < 0 {
		return ""
	}
	return body[endIdx : endIdx+dividerIdx]
}

// splitSourcesCSV splits a comma-separated list of
// citation URLs into a slice. Used by the sources-as-badges
// test to seed the fake service.
func splitSourcesCSV(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
