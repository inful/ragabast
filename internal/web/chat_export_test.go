package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// TestHandleChatExport_DownloadsMarkdown pins the
// happy-path HTTP contract: GET /api/chat/export
// streams the transcript as text/markdown with a
// Content-Disposition that triggers a browser
// download. Operators should be able to click the
// "Export transcript" link and save the conversation
// without typing anything.
func TestHandleChatExport_DownloadsMarkdown(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		transcriptByID: map[string]string{
			"sess-1": "## User\n\nHello\n\n## Assistant\n\nWorld",
		},
	}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/chat/export?session_id=sess-1", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/markdown; charset=utf-8",
		rec.Header().Get("Content-Type"),
		"Content-Type must be markdown so browsers treat the file correctly")
	disp := rec.Header().Get("Content-Disposition")
	require.Contains(t, disp, "attachment",
		"Content-Disposition must trigger a download, not render inline")
	require.Contains(t, disp, ".md",
		"filename must end in .md so the OS picks the right handler")
	require.Contains(t, disp, "sess-1",
		"filename must include the session id so multiple exports don't collide")
	require.Equal(t, "## User\n\nHello\n\n## Assistant\n\nWorld",
		rec.Body.String(),
		"body must be the transcript verbatim — no transformation")
}

// TestHandleChatExport_EmptyTranscriptIsNotFound pins
// the 404 path: a session that has no messages returns
// 404, not an empty .md file. Operators who click the
// export link on a fresh chat should see a clear
// "nothing to export" message instead of a blank file
// that looks like a successful download.
func TestHandleChatExport_EmptyTranscriptIsNotFound(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{} // no transcripts configured → all empty
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/chat/export?session_id=unknown", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code,
		"empty transcripts must 404 — never serve an empty .md file")
}

// TestHandleChatExport_MissingSessionIDIsNotFound pins
// the no-session-id case: a request with no session_id
// (e.g. someone bookmarked the URL and lost the query
// string) must NOT return every transcript. 404 is the
// right signal — there's nothing to export.
func TestHandleChatExport_MissingSessionIDIsNotFound(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/chat/export", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code,
		"missing session_id must NOT serve content — every transcript is private to its session")
}

// TestHandleChatExport_DoesNotMutateSession pins the
// read-only contract: GET /api/chat/export must NOT
// modify the session store. The handler must only read;
// any append/clear side-effect would surprise operators
// who expected an idempotent "save my work" action.
func TestHandleChatExport_DoesNotMutateSession(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		transcriptByID: map[string]string{"s": "## User\n\nx"},
	}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/chat/export?session_id=s", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, svc.appendedTurns,
		"export must not append to the session — read-only")
	require.Empty(t, svc.clearedSessions,
		"export must not clear the session — read-only")
}

// TestHandleChatExport_FilenameHasMarkdownExtension
// pins the filename contract: the attachment filename
// must end in .md so OS file handlers recognize it.
// Without this, a browser might save it as .bin or .txt
// and break the operator's wiki paste.
func TestHandleChatExport_FilenameHasMarkdownExtension(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		transcriptByID: map[string]string{"deadbeef-1234": "x"},
	}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/chat/export?session_id=deadbeef-1234", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	disp := rec.Header().Get("Content-Disposition")
	require.Contains(t, disp, ".md")
	require.Contains(t, disp, "deadbeef",
		"filename must include the session id prefix so multiple exports are distinguishable")
	// Make sure we ALSO serve the body — pinning the
	// filename in isolation would let an empty body
	// slip past the test if Content-Type were wrong.
	require.Equal(t, "x", rec.Body.String())
}

// Sanity check: the existing append/clear paths still
// work after the ExportChatTranscript method was added.
// Without this, the fakes could have accidentally
// shadowed AppendChatTurn.
func TestHandleChatExport_ChatMessageStillAppendsAfterExport(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeHumaService{answer: "ok"}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message",
		strings.NewReader("csrf_token=test&message=hi&session_id=s1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, svc.appendedTurns, 1,
		"chat/message must still record the exchange — regression guard")
	_ = service.ChatMessage{} // keep the import alive
}
