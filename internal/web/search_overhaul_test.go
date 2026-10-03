package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// searchOverhaulTestServer builds the standard
// search-test Server fixture for the Phase 3 tests.
func searchOverhaulTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
	return s
}

// searchFormBody fetches the /search page body
// (the GET form) for the Phase 3 tests.
func searchFormBody(t *testing.T, s *Server) string {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/search", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	return w.Body.String()
}

// TestSearch_ResultCountIsSelect pins the Phase 3.4
// contract from plans/ux-overhaul.md: the "Result
// count" field is a <select> with discrete options
// (5 / 10 / 20 / 50) rather than a free-form
// <input type="number">. The free-form input lets
// users type arbitrary values; the server-side clamp
// silently caps them at 50. The select makes the
// valid options explicit and removes the "I typed
// 100 and got 50" surprise.
func TestSearch_ResultCountIsSelect(t *testing.T) {
	body := searchFormBody(t, searchOverhaulTestServer(t))

	// The result-count field must be a <select>
	// with name="top_k". The input[type=number]
	// pattern is gone.
	assert.Regexp(t, `<select[^>]*name="top_k"`, body,
		"the result-count field must be a <select name=\"top_k\"> (Phase 3.4 contract)")

	// The select must carry the four canonical
	// options (5 / 10 / 20 / 50). We don't pin
	// whether 5 is the default (that's a
	// separate test); we pin that the four
	// options exist as <option> children.
	for _, opt := range []string{"5", "10", "20", "50"} {
		assert.Contains(t, body, `<option value="`+opt+`"`,
			"the result-count select must offer the value %s as a discrete option (Phase 3.4 contract)", opt)
	}

	// The legacy <input type="number" name="top_k">
	// pattern is gone.
	assert.NotRegexp(t, `<input[^>]*type="number"[^>]*name="top_k"`, body,
		"the legacy <input type=\"number\" name=\"top_k\"> pattern must be gone (Phase 3.4 replaces with a <select>)")
}

// TestSearch_SubmitButtonIsPrimary pins the Phase 3.6
// contract: the Search submit button uses
// btn-primary (the daisyUI primary action color),
// not btn-info. The pre-Phase-3.6 design used
// btn-info (cyan); the primary action convention
// reserves the brightest color for the action the
// user came to do.
func TestSearch_SubmitButtonIsPrimary(t *testing.T) {
	body := searchFormBody(t, searchOverhaulTestServer(t))

	// The submit button must use btn-primary.
	// We don't pin the button text (a future
	// redesign could say "Find" or "Search
	// again"), just the action class. The
	// regex tolerates the class / type
	// attributes appearing in any order (HTML5
	// allows either).
	assert.Regexp(t, `<button[^>]*?(?:class="[^"]*\bbtn-primary\b[^"]*"[^>]*?type="submit"|type="submit"[^>]*?class="[^"]*\bbtn-primary\b)`, body,
		"the Search submit button must use daisyUI's btn-primary (Phase 3.6 contract)")

	// The legacy btn-info submit button is gone.
	assert.NotRegexp(t, `<button[^>]*class="[^"]*\bbtn-info\b[^"]*"[^>]*type="submit"`, body,
		"the legacy btn-info Search button must be gone (Phase 3.6 replaces with btn-primary)")
}

// TestSearch_ActiveFiltersRenderChips pins the
// Phase 3.1 contract: when the search results have
// filters applied (tag, category, document_id), the
// results fragment renders a row of removable chips
// above the results. The pre-Phase-3.1 design just
// showed a "tag: foo" / "category: bar" / "doc: baz"
// row of static badges — clicking did nothing.
// Phase 3.1 turns each chip into a link to the
// same search with the filter removed, so the
// operator can iteratively narrow / widen.
//
// The test POSTs to /search with filters applied
// and asserts the chips render as anchors linking
// to the same query with the filter removed.
func TestSearch_ActiveFiltersRenderChips(t *testing.T) {
	s := searchOverhaulTestServer(t)

	// POST with a tag filter. The results fragment
	// should render a "tag: foo" chip linking to
	// the same search with the tag removed.
	form := "query=foo&tag=alpha&csrf_token=test"
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/search",
		strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "ragabast_csrf=test")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	// The fake service returns no results; the
	// fragment still renders the active-filter
	// chips before the empty-state alert.
	require.Equal(t, 200, w.Code)
	body := w.Body.String()

	// The chip must render as an <a> (clickable)
	// that links to a /search URL. The link
	// removes the tag filter (so the chip's
	// href omits the tag parameter) — that's
	// the whole point of Phase 3.1: clicking
	// the chip removes the filter.
	//
	// The exact href format is
	// htmx-driven; we just pin the structural
	// "clickable <a> linking to /search with
	// 'tag: alpha' as the chip text" property.
	assert.Regexp(t, `<a[^>]*href="/search\?[^"]*"[^>]*>\s*tag:\s*alpha`, body,
		"the active filter chip must be a clickable <a> linking to /search (Phase 3.1 contract)")

	// The legacy static <span> chip is gone.
	// The pre-Phase-3.1 design used
	// <span class="badge badge-info">tag: alpha</span>
	// (a non-clickable span). The new design uses
	// <a> elements.
	assert.NotRegexp(t, `<span[^>]*class="[^"]*\bbadge-info\b[^"]*"[^>]*>\s*tag:\s*alpha`, body,
		"the legacy <span class=\"badge badge-info\"> static chip must be gone (Phase 3.1 replaces with clickable <a>)")
}

// TestSearch_NoResultsHasAddDocumentCTA pins the
// Phase 3.3 contract: when the search returns no
// results, the no-results alert includes a CTA
// link to the ingest guide (the same link the
// documents page's empty state uses). Without
// the CTA, an operator who searched and found
// nothing has no in-page next step.
func TestSearch_NoResultsHasAddDocumentCTA(t *testing.T) {
	s := searchOverhaulTestServer(t)

	// POST a query that returns no results.
	// (The fake service returns no SearchResult
	// by default.)
	form := "query=nonexistent&csrf_token=test"
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/search",
		strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "ragabast_csrf=test")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	body := w.Body.String()

	// The no-results alert must render.
	assert.Contains(t, body, "No results",
		"empty search results must surface a 'no results' affordance")

	// The CTA must link to the ingest guide.
	assert.Contains(t, body, `href="/docs#/operations/ingest"`,
		"the no-results alert must include a CTA link to the ingest guide (Phase 3.3 contract)")
}
