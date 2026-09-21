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
func TestDocumentsPage_EmptyCorpus(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Contains(t, body, "No documents ingested yet",
		"empty state must render the placeholder")
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
