package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestHandleIngestSubmit_ValidationError_RerendersForm pins the
// headline fix for issue #83: when the ingest pipeline rejects
// the content for a deterministic client-side reason (missing UID,
// missing fingerprint, malformed frontmatter, oversize body, etc.)
// the user must see the actual reason on the page so they can
// correct it. Today the response is `text/plain "Internal server
// error"`, which is indistinguishable from a real server crash.
func TestHandleIngestSubmit_ValidationError_RerendersForm(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		ingestErr: fmt.Errorf("wrapped: %w", models.ErrMissingUID),
	}
	s := NewServer(cfg, svc)

	body := strings.NewReader("content=not-frontmatter-at-all")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a missing-UID validation failure is a 4xx, not a 5xx")
	require.Contains(t, w.Header().Get("Content-Type"), "text/html",
		"the form re-render is HTML, not text/plain")
	body2 := w.Body.String()
	require.Contains(t, body2, "document UID is required",
		"the user must see the actual validation message, not a generic 500")
	// The textarea must be preserved so the user can correct and resubmit.
	require.Contains(t, body2, "not-frontmatter-at-all",
		"the original submission should survive the error round-trip")
}

// TestHandleIngestSubmit_OversizeBody_400 pins the size-cap path:
// the oversize check returns http.Error today. After the fix it
// should still be a 400 with a useful message, and crucially the
// response should be HTML (so htmx swaps it in) not text/plain.
func TestHandleIngestSubmit_OversizeBody_400(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.MaxIngestDocumentBytes = 100
	svc := &fakeService{} // happy path; we don't even reach IngestDocument
	s := NewServer(cfg, svc)

	huge := strings.Repeat("x", 200)
	body := strings.NewReader("content=" + huge)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "text/html",
		"oversize must surface as a form re-render so the user can see what went wrong")
	require.Contains(t, w.Body.String(), "exceeds")
}

// TestHandleIngestSubmit_ServerError_KeepsGeneric500 pins the
// other side of the fix: genuine server-side failures (embedding
// generation crashed, vector DB unreachable) must still return a
// generic 500. The detail belongs in the server log, not in the
// browser, since it can leak server URLs / model names / stack
// traces to the operator's session. The key behavioral change
// from before the fix: the response is no longer `text/plain`
// with a body that pretends nothing went wrong — it's a 500 with
// a request_id the operator can grep the log for.
func TestHandleIngestSubmit_ServerError_KeepsGeneric500(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		ingestErr: errors.New("vector db unreachable"),
	}
	s := NewServer(cfg, svc)

	body := strings.NewReader("content=---\nuid: x\n---\nbody")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotEmpty(t, w.Header().Get("X-Request-ID"),
		"every 500 must carry a request id so the operator can grep the log")
	require.NotContains(t, w.Body.String(), "vector db unreachable",
		"the raw error must not leak to the client")
}

// TestHandleIngestSubmit_HappyPath_StillRendersSuccess pins that
// the existing happy path is unaffected by the error-handling
// change.
func TestHandleIngestSubmit_HappyPath_StillRendersSuccess(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		ingestDocument: &models.Document{
			ID:     "happy-doc",
			Tags:   []string{"t1"},
			Chunks: []models.Chunk{{}, {}},
		},
	}
	s := NewServer(cfg, svc)

	body := strings.NewReader("content=---\nuid: happy-doc\n---\nbody")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "happy-doc",
		"successful ingest must still render the ingest_success page")
}
