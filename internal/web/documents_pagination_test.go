package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestDocumentsPage_RendersPaginationLinks pins the
// /documents UI contract: when the corpus exceeds the
// page size, the operator sees Previous / Next links
// that round-trip through the same ?limit=&offset=
// query params as the JSON endpoint. This is what keeps
// a 10k-corpus browser session bounded — without these
// links, the operator would have to manually edit the URL
// to page through the data.
func TestDocumentsPage_RendersPaginationLinks(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{documents: docsForPagination(60)})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Contains(t, body, "Showing",
		"page must render a 'Showing N of M' summary")
	require.Contains(t, body, `href="/documents?limit=25&offset=25"`,
		"page must render a Next link with limit=25&offset=25 (got %s)", body)
	require.NotContains(t, body, `href="/documents?limit=25&offset=0"`,
		"page must NOT render a Previous link on the first page")
}

// TestDocumentsPage_NextPageRoundTrip pins the
// end-to-end contract: clicking Next reloads the page
// with offset=25 and shows docs 26-50. Same logic as the
// JSON endpoint, just rendered as HTML.
func TestDocumentsPage_NextPageRoundTrip(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{documents: docsForPagination(60)})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents?limit=25&offset=25", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Contains(t, body, "doc-00025",
		"page 2 must start at doc-00025")
	require.Contains(t, body, `href="/documents?limit=25&offset=0"`,
		"page 2 must render Previous link back to offset=0")
}

// TestDocumentsPage_EmptyCorpus pins the empty-state
// branch: no documents means no pagination links at
// all (rather than "Showing 0–0 of 0"). The empty
// placeholder is rendered by the table branch.
//
// Phase 6.1 of plans/ux-overhaul.md updated the
// empty-state copy from "No documents ingested yet"
// to "Nothing here yet." as part of the cross-cutting
// polish pass.
func TestDocumentsPage_EmptyCorpus(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Contains(t, body, "Nothing here yet",
		"empty state must surface the Phase 6.1 copy (Nothing here yet.)")
	require.NotContains(t, body, "Showing",
		"empty state must NOT render pagination summary")
}

// TestDocumentsPage_PageSizeOneRendersManyPages pins
// the boundary where the corpus is larger than a single
// page: 60 docs with limit=1 produces 60 pages and the
// Next link points to offset=1 (the second page).
func TestDocumentsPage_PageSizeOneRendersManyPages(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{documents: docsForPagination(60)})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents?limit=1&offset=0", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Contains(t, body, `href="/documents?limit=1&offset=1"`,
		"limit=1 page 0 must have Next pointing to offset=1")
}

// docsForPagination is a test helper that builds a
// corpus of n deterministic DocumentInfo entries so
// pagination tests are reproducible. IDs are zero-padded
// so lexicographic sort matches numeric sort.
func docsForPagination(n int) []models.DocumentInfo {
	out := make([]models.DocumentInfo, n)
	for i := range n {
		out[i] = models.DocumentInfo{
			ID:    fmt.Sprintf("doc-%05d", i),
			UID:   fmt.Sprintf("u-%05d", i),
			Title: fmt.Sprintf("Document %05d", i),
		}
	}
	return out
}

// TestDocumentsPage_NoTitleShowsFilenameFallback pins the fix
// for issue #84: a document with no Title and a known FilePath
// must surface the filename (basename without extension) as the
// row's title cell. Before the fix, /documents left the cell
// empty, which looked like a render bug. After the fix, the
// /documents template uses DocumentInfo.DisplayLabel which
// shares the title → filename → id chain used by chat sources
// and search results.
func TestDocumentsPage_NoTitleShowsFilenameFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = "" // force the unsafe fallback path
	s := NewServer(cfg, &fakeService{documents: []models.DocumentInfo{
		{ID: "doc-without-title", UID: "doc-without-title", FilePath: "/tmp/data/documents/untitled-ramble.md"},
	}})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Contains(t, body, "untitled-ramble",
		"a doc with no Title must show its filename (basename without extension) in the row")
	// Pin the row shape (Title cell populated, ID code present)
	// so a regression in the template (e.g. switching back to
	// {{ .Title }} which is empty here) shows up immediately.
	// We use a regex because html/template keeps the source
	// whitespace between adjacent {{ }} interpolations.
	require.Regexp(t, `<td>\s*untitled-ramble\s*</td>\s*<td><code>\s*doc-without-title\s*</code>`,
		body, "the no-title fallback must populate the Title cell next to the ID chip")
}
