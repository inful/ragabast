package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// TestHandleChatMessage_SourcesPanelOpenByDefault pins the fix
// for issue #86: the sources disclosure must render with `open`
// so the citations the system worked to surface are visible to
// the operator the moment the chat reply lands. Before the fix,
// the disclosure was collapsed by default and the small
// "N sources" summary was easy to miss entirely.
//
// The test asserts two things at once:
//   - the <details> element carries the open attribute (or
//     <details open> serialized markup) so the browser renders
//     it expanded
//   - the body of the disclosure (the <ol> with each source) is
//     visible without any client-side script — the rendered HTML
//     must contain the per-source labels
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

	require.Regexp(t, `<details[^>]*\bopen\b`,
		body, "the sources disclosure must render with the open attribute so citations are visible without a click")
	// Source labels are inside the <details>; they are emitted
	// in the rendered HTML whether the element is collapsed or
	// not, so this assertion also confirms the <ol> follows the
	// <summary> rather than being hidden behind a click.
	require.Contains(t, body, "ADR 001",
		"the source label must appear in the rendered body, inside the disclosure")
}

// TestHandleChatMessage_SourcesPanelInsideAssistantReply pins
// the second half of the fix: the disclosure must be visually
// attached to the assistant's reply box, not floating between
// the reply and the input form. We assert structural containment
// — the <details> element lives inside the same <div
// class="box has-background-light"> that wraps the assistant
// reply, not as a sibling. A future template edit that lifts
// the disclosure back out surfaces the regression immediately.
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

	assistantBoxOpen := strings.Index(body, `<div class="box has-background-light">`)
	require.GreaterOrEqual(t, assistantBoxOpen, 0, "assistant box must be present")

	assistantContentIdx := strings.Index(body, "Assistant")
	require.Greater(t, assistantContentIdx, assistantBoxOpen,
		"Assistant heading must come after the assistant box opens")
	detailsIdx := strings.Index(body, "<details")
	require.Greater(t, detailsIdx, assistantContentIdx,
		"sources disclosure must follow the Assistant heading (be inside the assistant box)")

	// The </div> that closes the assistant box must come after
	// the </details>. Find each closing tag after the <details>
	// and assert at least one pair of <details>...</details>
	// lives entirely inside the assistant box.
	detailsCloseIdx := strings.Index(body[detailsIdx:], "</details>")
	require.Positive(t, detailsCloseIdx)
	detailsEnd := detailsIdx + detailsCloseIdx + len("</details>")
	// After </details>, look for the next </div> — that should
	// be the close of the assistant box. Find the </div> that
	// closes the outer content div too, to confirm the
	// disclosure is inside the assistant box, not after it.
	assistantBoxCloseIdx := strings.Index(body[detailsEnd:], "</div>")
	require.Positive(t, assistantBoxCloseIdx,
		"assistant box must have a closing </div> after the sources disclosure")
}
