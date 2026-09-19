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

// TestHandleChatMessage_RendersSourcesWithDocbuilderURL pins the
// contract that the chat UI surfaces the source documents
// (with their docbuilder permalink when ragabast.docbuilder_base_url
// is configured). Before this fix, handleChatMessage discarded
// QueryDebugInfo.Results entirely, so chat had no source list
// at all.
//
// The test seeds two sources, one with a docbuilder URL and one
// without. The rendered HTML must:
//   - show a sources panel (some element labeled "sources")
//   - include the docbuilder URL as a clickable link for the
//     source that has one
//   - fall back to the document_id for the source that doesn't
func TestHandleChatMessage_RendersSourcesWithDocbuilderURL(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "https://docs.example.com"
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
					DocbuilderURL: "https://docs.example.com/_uid/adr-001/",
				},
				{
					ChunkID:       "c2",
					DocumentID:    "doc-2",
					DocumentTitle: "ADR 002",
					UID:           "adr-002",
					Similarity:    0.74,
					// No DocbuilderURL — the doc either has no UID
					// or the base URL isn't configured. Either way
					// the UI must fall back to a document_id chip
					// so the user can still see what was cited.
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

	// The user message and the assistant reply must still appear.
	require.Contains(t, body, "what does adr-001 say")
	require.Contains(t, body, "Here is what I found.")

	// The source list must surface both docs. The titles win
	// over the UIDs in the template (more user-friendly), so we
	// assert on the title for doc-2 and on the title for doc-1
	// (whose DocbuilderURL also contains the UID path).
	require.Contains(t, body, "ADR 001",
		"the source title must appear in the rendered sources")
	require.Contains(t, body, "ADR 002",
		"the source title must appear in the rendered sources")

	// The source with a docbuilder URL must render as a link.
	require.Contains(t, body, "https://docs.example.com/_uid/adr-001/",
		"the docbuilder URL must surface as a clickable link in the chat sources panel")

	// The source without one must fall back to the document_id,
	// not to a broken link or to nothing.
	require.Contains(t, body, "doc-2",
		"sources without a docbuilder URL must fall back to the document_id")
}

// TestHandleChatMessage_NoSources_OmitsPanel pins the contract
// that the chat UI does NOT render a sources panel when the
// answer has zero sources (e.g. a chat that didn't trigger any
// retrieval). A spurious empty "Sources:" header would be
// worse than no panel at all.
func TestHandleChatMessage_NoSources_OmitsPanel(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		queryAnswer: "I don't know.",
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{}, // explicit empty
		},
	}
	s := NewServer(cfg, svc)

	form := "message=hi"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// The "Sources" label must not appear when there are none.
	require.NotContains(t, strings.ToLower(body), "source",
		"a sources panel must not appear when Results is empty")
}
