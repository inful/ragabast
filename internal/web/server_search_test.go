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

// TestHandleSearchPage_GET_UsesDaisyUIComponents pins the
// post-migration contract for the /search form (Phase 2c of
// the Bulma -> DaisyUI migration; see
// plans/daisyui-migration.md). The form moves from Bulma's
// field/control wrapper soup to daisyUI's idiomatic
// pattern: each field is wrapped in a `fieldset` with a
// `fieldset-legend`, the input/select carries the daisyUI
// `input` / `select` / `checkbox` / `btn` class directly,
// and the two-column layout (tag + category) is a Tailwind
// grid (not Bulma's `columns/column`).
//
// The page also gains a `<link>` to daisyui.min.css so the
// new class names are actually styled — the migration is
// progressive, but each sub-phase must leave the page in a
// renderable state, not a "markup is right but everything is
// unstyled" state. Bulma stays loaded until Phase 3.
//
// This test is a regression guard for the migration: a
// future contributor who reaches for Bulma's `field/control`
// wrappers (or any of the other pre-migration class names)
// gets a red test, with a comment that points at the SPEC
// and explains the daisyUI alternative.
func TestHandleSearchPage_GET_UsesDaisyUIComponents(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// daisyUI components used in the form. The exact
	// class strings (e.g. "input w-full" vs "input") may
	// differ from contributor to contributor; the test
	// pins the component class name as a token, not the
	// literal class attribute.
	require.Contains(t, body, `daisyui.min.css`,
		"the page must load daisyui.min.css so the new daisyUI class names are actually styled (Phase 2c)")

	// Use word-boundary regex so the assertions don't
	// match substrings of unrelated class names.
	require.Regexp(t, `\bclass="[^"]*\binput\b`, body,
		"the query/document_id/top_k fields must use daisyUI's `input` class on a <input> element")
	require.Regexp(t, `\bclass="[^"]*\bselect\b`, body,
		"the tag/category fields must use daisyUI's `select` class on a <select> element")
	require.Regexp(t, `\bclass="[^"]*\bbtn-info\b`, body,
		"the submit button must use daisyUI's `btn btn-info` class")
	require.Regexp(t, `<input type="checkbox"[^>]*\bclass="[^"]*\bcheckbox\b`, body,
		"the source-kind checkboxes must use daisyUI's `checkbox` class on the <input>")
	require.Regexp(t, `<fieldset[^>]*\bclass="[^"]*\bfieldset\b`, body,
		"the form must group related fields with daisyUI's `fieldset` component")

	// Anti-regression: no Bulma class names should remain
	// in the form. The page chrome (navbar) is still Bulma
	// in this phase, so we only assert that the form's
	// own structure has no Bulma wrappers.
	require.NotRegexp(t, `class="field"`, body,
		"the form must not use Bulma's `field` wrapper (daisyUI uses `fieldset` instead)")
	require.NotRegexp(t, `class="control"`, body,
		"the form must not use Bulma's `control` wrapper (daisyUI inputs carry their own class)")
	require.NotRegexp(t, `class="select is-fullwidth"`, body,
		"the form must not use Bulma's `select is-fullwidth` (daisyUI's `select w-full` is the equivalent)")
	require.NotRegexp(t, `<button[^>]*class="button is-info"`, body,
		"the submit button must not use Bulma's `button is-info` (daisyUI's `btn btn-info` is the equivalent)")
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

// TestHandleSearchPage_RendersDocumentIDAutocomplete pins the
// fix for issue #90: the Document ID filter on /search now
// carries a <datalist> of the corpus's UIDs and titles so the
// browser can autocomplete the field. Before the fix the
// field was a plain text input — the operator had to remember
// the exact UID or copy it from /documents. The datalist is
// populated from ListDocumentsPaged on every page render (a
// paginated walk so a 10k-corpus operator doesn't pull every
// doc into the page chrome).
func TestHandleSearchPage_RendersDocumentIDAutocomplete(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{documents: []models.DocumentInfo{
		{ID: "arch-overview", UID: "arch-overview", Title: "Architecture Overview"},
		{ID: "deployment-guide", UID: "deployment-guide", Title: "Deployment Guide"},
		{ID: "doc-without-title", UID: "doc-without-title"}, // no title — should still appear by UID
	}})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, `<datalist id="document_id_options">`,
		"/search must render a datalist to power Document ID autocomplete")
	require.Contains(t, body, `<option value="arch-overview">`,
		"the datalist must include each document's UID as an option value")
	require.Contains(t, body, `<option value="deployment-guide">`,
		"every doc in the corpus must appear, not just the first")
	require.Contains(t, body, `<option value="doc-without-title">`,
		"docs without a Title must appear by UID alone (no skipped rows)")
	require.Contains(t, body, `list="document_id_options"`,
		"the Document ID input must reference the datalist via list= so the browser can autocomplete")
}

// TestHandleSearchPage_NoDocuments_OmitsDatalist pins the
// empty-corpus branch: with zero documents the datalist is
// omitted (rather than rendered as an empty <datalist></datalist>
// that some browsers render as a visible empty dropdown).
func TestHandleSearchPage_NoDocuments_OmitsDatalist(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), `<datalist id="document_id_options">`,
		"with no documents the datalist must be omitted entirely")
}

// TestHandleSearchSubmit_EmptyQuery_RendersNotification pins the
// input contract: an empty query is a client-visible error, not a
// search. The handler returns 200 with a Bulma notification fragment
// so htmx can swap it into #search-results; the service is never
// called.
func TestHandleSearchSubmit_EmptyQuery_RendersNotification(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{}
	s := NewServer(cfg, svc)

	form := url.Values{}
	// no query field

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"empty-query POST must return 200 so htmx can swap the notification into the target")
	body := w.Body.String()
	require.Contains(t, body, "Query is required",
		"empty-query response must contain a user-readable error")
	require.Empty(t, svc.lastSearchQuery, "an empty query must not reach the service")
}

// TestHandleSearchSubmit_RendersFilenameFallback pins the fix for
// the "search results show the UUID" complaint: a result with no
// DocumentTitle but with a known file path renders the basename
// (without extension) as the heading. The template uses
// SearchResult.DisplayLabel so the fallback chain matches the
// inline-link text in service.InlineSourceLinks.
func TestHandleSearchSubmit_RendersFilenameFallback(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{
				ChunkID:          "c1",
				DocumentID:       "doc-1",
				DocumentFilePath: "/var/docs/adr-001.md",
				HeaderPath:       "Introduction",
				Level:            1,
				Content:          "ragabast is a RAG service",
				Similarity:       0.87,
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "what is ragabast")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	require.NotPanics(t, func() {
		s.router.ServeHTTP(w, req)
	})

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The friendly filename must appear as the result heading.
	require.Contains(t, body, "adr-001",
		"a result with no title must show the filename (basename without extension) as the heading")
}

// TestHandleSearchSubmit_RendersDaisyUIResults pins the
// post-migration shape of the results fragment. Each
// result renders inside a daisyUI `card` (replacing
// Bulma's `box`); the filter chips use daisyUI's
// `badge badge-info` (replacing Bulma's `tag is-info`);
// the "New Search" link uses daisyUI's `btn`.
//
// Phase 2d of the Bulma -> DaisyUI migration. The
// fragment is rendered as the htmx response body and
// swapped into #search-results on the /search page; the
// parent page already loads daisyui.min.css (Phase 2c).
func TestHandleSearchSubmit_RendersDaisyUIResults(t *testing.T) {
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
	form.Set("tag", "tut")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Filter chips use daisyUI's `badge badge-info` (was
	// Bulma's `tag is-info`).
	require.Regexp(t, `<span[^>]*\bclass="[^"]*\bbadge-info\b`, body,
		"filter chips must use daisyUI's `badge badge-info` class")

	// Each result is wrapped in a daisyUI `card` (was
	// Bulma's `box`).
	require.Regexp(t, `<div[^>]*\bclass="[^"]*\bcard\b`, body,
		"results must be wrapped in daisyUI's `card` class")

	// The "New Search" link uses daisyUI's `btn` (was
	// Bulma's `button is-small`).
	require.Regexp(t, `<a[^>]*\bclass="[^"]*\bbtn\b`, body,
		"the 'New Search' link must use daisyUI's `btn` class")

	// Anti-regression: no Bulma class names.
	require.NotRegexp(t, `class="tag is-info"`, body,
		"the results fragment must not use Bulma's `tag is-info` (daisyUI's `badge badge-info` is the equivalent)")
	require.NotRegexp(t, `class="box"`, body,
		"the results fragment must not use Bulma's `box` (daisyUI's `card` is the equivalent)")
	require.NotRegexp(t, `class="button is-small"`, body,
		"the new-search link must not use Bulma's `button is-small` (daisyUI's `btn btn-sm` is the equivalent)")
	require.NotRegexp(t, `class="title is-5"`, body,
		"the result heading must not use Bulma's `title is-5` (daisyUI's `text-xl font-semibold` is the equivalent)")
}

// TestHandleSearchSubmit_NoResults_UsesDaisyUIAlert pins the
// empty-state path. When the service returns no results, the
// response fragment renders daisyUI's `alert alert-warning`
// (the equivalent of the pre-migration Bulma `notification
// is-warning`).
//
// Phase 2d of the Bulma -> DaisyUI migration (see
// plans/daisyui-migration.md) swapped Bulma's
// `notification is-warning` for daisyUI's `alert
// alert-warning`. The fragment is rendered as the htmx
// response body; daisyui.min.css is already loaded by the
// parent /search page (Phase 2c).
func TestHandleSearchSubmit_NoResults_UsesDaisyUIAlert(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "nothing should match this")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// DaisyUI's `alert alert-warning` is the daisyUI
	// equivalent of Bulma's `notification is-warning`.
	require.Regexp(t, `<div[^>]*\bclass="[^"]*\balert-warning\b`, body,
		"the no-results fragment must use daisyUI's `alert alert-warning` class")
	require.NotRegexp(t, `class="notification`, body,
		"the empty-state must not use Bulma's `notification` (daisyUI's `alert` is the equivalent)")
}
