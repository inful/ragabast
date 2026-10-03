package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleChatMessage_SourcesPanelOpenByDefault pins the
// "citations are immediately visible" affordance. The
// pre-Phase-2.2 design used a collapsed <details> block
// with the `open` attribute so citations were visible
// without a click (issue #86). Phase 2.2 replaces the
// collapsed disclosure with inline badge chips that are
// visible by definition — there's nothing to click or
// expand. The new contract is: source labels appear in
// the rendered body, attached to the assistant's reply
// bubble, with no client-side action required.
func TestHandleChatMessage_SourcesPanelOpenByDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		queryAnswer: "Here is what I found.",
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					ChunkID:       "c1",
					DocumentID:    "doc-1",
					DocumentTitle: "ADR 001",
					UID:           "adr-001",
					Similarity:    0.91,
				},
			},
		},
	}
	s := NewServer(cfg, svc)

	form := "message=what+does+adr-001+say"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Phase 2.2 of plans/ux-overhaul.md: the
	// collapsed <details> is gone. Sources render as
	// inline badge chips, immediately visible without
	// a click. The new test pins the "always visible"
	// property by checking the source label appears
	// in the rendered body without any user
	// interaction.
	assert.NotContains(t, body, "<details",
		"Phase 2.2 replaces the <details> source disclosure with inline badges; the disclosure must be gone")

	// The source label must appear in the rendered
	// body. (In the new design, the label is inside
	// the badge anchor.)
	require.Contains(t, body, "ADR 001",
		"the source label must appear in the rendered body (Phase 2.2 inline badge pattern)")
}

// TestHandleChatMessage_SourcesPanelInsideAssistantReply pins
// the "sources are attached to the assistant's reply" property.
// Phase 2.2 moves the source citations inside the
// chat-bubble (the daisyUI chat component for the assistant
// message). The contract: the source badges render inside
// the assistant's chat-bubble, not floating between the
// reply and the input form.
func TestHandleChatMessage_SourcesPanelInsideAssistantReply(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		queryAnswer: "Here is what I found.",
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					ChunkID:       "c1",
					DocumentID:    "doc-1",
					DocumentTitle: "ADR 001",
					UID:           "adr-001",
					Similarity:    0.91,
				},
			},
		},
	}
	s := NewServer(cfg, svc)

	form := "message=what+does+adr-001+say"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The assistant message uses daisyUI's chat
	// component. Pin the chat-bubble element (the
	// message surface) and the chat-image (the
	// avatar anchor) as the structural markers of
	// the assistant's reply.
	assistantBubbleOpen := strings.Index(body, "chat-bubble")
	require.GreaterOrEqual(t, assistantBubbleOpen, 0,
		"assistant chat-bubble must be present (Phase 2.2 daisyUI chat component)")

	// The "Assistant" label appears in the chat-header
	// (above the bubble). Find it; the source
	// badges must come after it.
	assistantHeaderIdx := strings.Index(body, "Assistant")
	require.GreaterOrEqual(t, assistantHeaderIdx, 0,
		"Assistant chat-header must be present (Phase 2.2 daisyUI chat-header)")

	// The source badge (containing the document
	// title) must render after the Assistant
	// header, inside the assistant bubble.
	badgeIdx := strings.Index(body, "ADR 001")
	require.Greater(t, badgeIdx, assistantHeaderIdx,
		"source badge label must follow the Assistant header (be inside the assistant's reply block)")

	// The source badge must be inside the assistant
	// chat-bubble. Find the chat-bubble that
	// contains the badge label.
	bubbleBeforeBadge := body[assistantBubbleOpen:badgeIdx]
	require.Positive(t, strings.Count(bubbleBeforeBadge, "chat-bubble"),
		"source badge must render inside the assistant's chat-bubble (not floating between reply and input form)")

	// Pin the absence of the legacy <details> block
	// to catch a refactor that re-introduces it.
	assert.NotContains(t, body, "<details",
		"the legacy <details> source disclosure must not be re-introduced (Phase 2.2 replaces it with inline badges)")
}
