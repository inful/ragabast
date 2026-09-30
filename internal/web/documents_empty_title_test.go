package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestDocumentsPage_NoTitleCellNeverEmpty pins the fix for
// issue #91 in combination with issue #84: every row in
// /documents must surface something in the Title cell — a
// plain title, a filename fallback, or at minimum the UID.
// The cell is never literally empty. The table-driven matrix
// below exercises the three branches of DocumentInfo.DisplayLabel
// (title / filename / UID) and asserts the Title cell carries
// the expected string.
//
// Before #84 the empty-title branch rendered an empty <td></td>
// which looked like a render bug; after #84 the filename fallback
// fills it. After #87 the table also carries a Delete column,
// so the regex below anchors on the leading <td> + the
// DisplayLabel value to avoid matching the empty Delete-cell
// markup.
func TestDocumentsPage_NoTitleCellNeverEmpty(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = "" // force fallback renderer
	s := NewServer(cfg, &fakeService{documents: []models.DocumentInfo{
		{ID: "doc-with-title", UID: "doc-with-title", Title: "Doc With Title"},
		{ID: "doc-without-title", UID: "doc-without-title", FilePath: "/tmp/data/documents/untitled-ramble.md"},
		{ID: "doc-only-uid", UID: "doc-only-uid"},
	}})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Regexp(t, `<td>\s*Doc With Title\s*</td>`,
		body, "the title branch must render the document title verbatim")
	require.Regexp(t, `<td>\s*untitled-ramble\s*</td>`,
		body, "the filename fallback must populate the Title cell when Title is empty (issue #84)")
	require.Regexp(t, `<td>\s*doc-only-uid\s*</td>`,
		body, "the UID branch must populate the Title cell when both Title and FilePath are empty")
}
