package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// chdirToRepoRoot switches the test's working directory to the
// repository root so config.DefaultConfig's template lookup
// (filepath.Join(wd, "templates")) finds templates/*.html at the
// repo root. Returns the cwd the caller can restore if useful;
// the change is automatically reverted when the test ends via
// t.Chdir's cleanup.
func chdirToRepoRoot(t *testing.T) {
	t.Helper()
	// The test binary runs from the package directory (internal/web).
	// Walk two parents up to land at the module root.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	t.Chdir(repoRoot)
}

// TestHandleSearchPage_GET_RendersFormWithFilters pins the /search
// form: GET returns 200, the body has a POST form with the four
// filter fields (query, tag, category, document_id), and the
// available tags/categories are surfaced as <option> elements so
// users don't have to guess.
func TestHandleSearchPage_GET_RendersFormWithFilters(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		tags:       []string{"tut", "ref"},
		categories: []string{"Guides", "Reference"},
	}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, `<form`, "GET /search must render a form")
	require.Contains(t, body, `action="/search"`, "form must POST to /search")
	require.Contains(t, body, `name="query"`, "form must carry a query input")
	require.Contains(t, body, `name="tag"`, "form must carry a tag filter")
	require.Contains(t, body, `name="category"`, "form must carry a category filter")
	require.Contains(t, body, `name="document_id"`, "form must carry a document_id filter")
	require.Contains(t, body, `<option value="tut"`, "available tags must surface as <option>s")
	require.Contains(t, body, `<option value="ref"`, "all available tags must surface")
	require.Contains(t, body, `<option value="Guides"`, "available categories must surface as <option>s")
}

// TestHandleSearchSubmit_BasicQuery_RendersResults pins the happy
// path: POST with just a query returns 200, calls the service with
// that query and empty filters, and renders the results without
// panicking on the []models.SearchResult type.
func TestHandleSearchSubmit_BasicQuery_RendersResults(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{
				ChunkID:       "c1",
				DocumentID:    "doc-1",
				DocumentTitle: "Getting Started",
				HeaderPath:    "Introduction",
				Level:         1,
				Content:       "ragabast is a RAG service",
				Similarity:    0.87,
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "what is ragabast")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	// Must not panic: previous to this fix the fallback path crashed
	// with "interface conversion: []models.SearchResult is not
	// []interface {}".
	require.NotPanics(t, func() {
		s.router.ServeHTTP(w, req)
	}, "POST /search must not panic on a typed []models.SearchResult slice")

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Equal(t, "what is ragabast", svc.lastSearchQuery, "handler must forward the form query to the service")
	require.Equal(t, service.SearchFilters{}, svc.lastSearchFilters, "no filter form fields set means an empty SearchFilters")
	require.Contains(t, body, "Getting Started", "result document title must appear in the rendered HTML")
	require.Contains(t, body, "ragabast is a RAG service", "result content must appear in the rendered HTML")
	require.Contains(t, body, "0.870", "result similarity score must appear in the rendered HTML")
}

// TestHandleSearchSubmit_PassesFilters pins the filter-passthrough
// contract: every filter form field that the user fills in is
// forwarded into the SearchFilters struct the service receives.
// This is the new behavior; before this fix the handler hardcoded
// SearchFilters{} regardless of the form.
func TestHandleSearchSubmit_PassesFilters(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "find me docs")
	form.Set("tag", "tutorial")
	form.Set("category", "Guides")
	form.Set("document_id", "doc-42")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "find me docs", svc.lastSearchQuery)
	require.Equal(t, "tutorial", svc.lastSearchFilters.Tag)
	require.Equal(t, "Guides", svc.lastSearchFilters.Category)
	require.Equal(t, "doc-42", svc.lastSearchFilters.DocumentID)
}

// TestHandleSearchSubmit_EmptyQuery_Returns400 pins the input
// contract: an empty query is a client error, not a search. The
// handler returns 400 before calling the service.
func TestHandleSearchSubmit_EmptyQuery_Returns400(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	form := url.Values{}
	// no query field

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Empty(t, svc.lastSearchQuery, "an empty query must not reach the service")
}
