package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestDocumentsPage_RendersDeleteButton pins the headline fix
// for issue #87: every row in /documents has a Delete form so
// the operator can remove a stale or wrongly-ingested document
// without falling back to the CLI. Before the fix, no such
// affordance existed in the UI.
func TestDocumentsPage_RendersDeleteButton(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = "" // force fallback renderer
	s := NewServer(cfg, &fakeService{documents: []models.DocumentInfo{
		{ID: "doc-1", UID: "doc-1", Title: "Doc 1"},
	}})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, `action="/documents/doc-1/delete"`,
		"each row must render a delete form posting to /documents/{id}/delete")
	require.Contains(t, body, `name="csrf_token"`,
		"delete form must carry the csrf token")
	require.Contains(t, body, `name="confirm"`,
		"delete form must require the operator to tick the confirm checkbox")
}

// TestDocumentsDelete_HappyPath deletes the targeted document
// and redirects back to the documents list. Pins that the row's
// action actually fires the service.DeleteDocument path.
func TestDocumentsDelete_HappyPath(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("csrf_token", "test-token")
	form.Set("confirm", "1")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/documents/doc-1/delete",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "ragabast_csrf=test-token")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code,
		"successful delete must redirect to /documents")
	loc, err := w.Result().Location()
	require.NoError(t, err)
	require.Equal(t, "/documents", loc.String())
	require.Equal(t, []string{"doc-1"}, svc.deletedIDs,
		"the handler must call svc.DeleteDocument with the targeted id")
}

// TestDocumentsDelete_RequiresConfirmation rejects deletes
// where the operator did not tick the confirm checkbox. This is
// the cheap native-HTML safeguard against accidental deletes
// (no JS budget, no modals): the form requires the checkbox
// before submit, so a bot or a forged POST that omits it gets
// a 400 and the document is preserved.
func TestDocumentsDelete_RequiresConfirmation(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("csrf_token", "test-token")
	// no confirm field

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/documents/doc-1/delete",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "ragabast_csrf=test-token")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"missing confirm must be a 400, not a destructive 200")
	require.Empty(t, svc.deletedIDs,
		"missing confirm must NOT call svc.DeleteDocument")
}

// TestDocumentsDelete_MissingID rejects empty doc ids — without
// this guard, /documents//delete would 404 (and look like a
// missing route to the operator) instead of an explicit 400.
func TestDocumentsDelete_MissingID(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("csrf_token", "test-token")
	form.Set("confirm", "1")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/documents//delete",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "ragabast_csrf=test-token")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"empty document id must be a 400, not a 404 from chi's mux")
	require.Empty(t, svc.deletedIDs)
}
