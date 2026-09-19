package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestHandleSearchPage_GET_FormIsHTMXDriven pins the contract that
// the form on GET /search uses htmx attributes (hx-post, hx-target,
// hx-swap) instead of doing a full-page POST + reload. The form
// must also carry hx-push-url so the URL bar reflects the active
// query (enables browser back/forward to undo a search).
//
// The tests deliberately do NOT chdir to the repo root: the
// templates are embedded so the binary works in any CWD.
func TestHandleSearchPage_GET_FormIsHTMXDriven(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = "" // force embedded templates
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The form must POST via htmx (hx-post), not via the default
	// browser POST + reload. A bare <form method="post"> would
	// trigger a full navigation and the test would still pass on
	// the rendered HTML, but UX would be wrong.
	require.Contains(t, body, `hx-post="/search"`,
		"the search form must declare hx-post so htmx intercepts the submit")
	require.Contains(t, body, `hx-target="#search-results"`,
		"the form must target the results container")
	require.Contains(t, body, `hx-swap="innerHTML"`,
		"the form must declare innerHTML swap")
	require.Contains(t, body, `hx-push-url="true"`,
		"the form must push the URL so back/forward navigates between searches")

	// The target div must exist in the initial GET so htmx has
	// somewhere to swap into.
	require.Contains(t, body, `id="search-results"`,
		"the GET response must contain a #search-results div for htmx to target")
}

// TestHandleSearchSubmit_HTMX_ReturnsFragment pins that POST
// /search returns ONLY the results fragment, not a full HTML
// page. htmx swaps the response into the target div, so a full
// page (with another <html> root, another set of <script>
// tags, etc.) would render badly.
//
// The test sends the HX-Request: true header to simulate an
// htmx-driven request. The handler does not actually inspect
// the header today — the contract is "always return the
// fragment" because the results page is a valid fragment
// regardless of who asked for it.
func TestHandleSearchSubmit_HTMX_ReturnsFragment(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	s := NewServer(cfg, &fakeService{
		searchResults: []models.SearchResult{
			{
				ChunkID: "c1", DocumentID: "doc-1", DocumentTitle: "HTMX Test",
				HeaderPath: "Section", Level: 1, Content: "fragment content", Similarity: 0.9,
			},
		},
	})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/search",
		strings.NewReader("query=hello"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true") // simulate htmx
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Must contain the fragment payload.
	require.Contains(t, body, "HTMX Test", "results fragment must contain the hit")
	require.Contains(t, body, "fragment content", "results fragment must contain the hit content")
	require.Contains(t, body, "0.900", "similarity score must appear")

	// Must NOT contain a full page wrapper — that would mean the
	// template is double-loading inside the target div.
	require.NotContains(t, body, `<!DOCTYPE html>`,
		"POST /search must not return a full HTML page; htmx swaps the response into #search-results")
	require.NotContains(t, body, `<script src="https://unpkg.com/htmx.org`,
		"the fragment must not load htmx a second time — that's already in the GET response")
	require.NotContains(t, body, `<form method="post"`,
		"the fragment must not contain a second form; htmx swaps into an existing one")
}

// TestHandleSearchSubmit_HTMX_EmptyQuery_SwapsError pins that an
// empty query does not silently 500 or render a broken fragment.
// htmx by default does NOT swap 4xx responses, so we return 200
// with an error notification fragment that htmx will swap in.
// The contract here is: empty query -> 200 + notification
// fragment, not 400 + plain text.
//
// If you change this contract (e.g. to keep returning 400 + plain
// text), update this test along with it.
func TestHandleSearchSubmit_HTMX_EmptyQuery_SwapsError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/search",
		strings.NewReader(""), // empty body -> no query field
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	// The form has `required` on the query input, so a real browser
	// will block the submit client-side. But programmatic submits
	// (curl, tests) bypass that. The server must still respond
	// cleanly.
	require.Equal(t, http.StatusOK, w.Code,
		"empty-query POST should return 200 so htmx swaps in an error fragment")
	body := w.Body.String()
	require.Contains(t, body, "Query is required",
		"empty-query response must contain a user-readable error message")
}
