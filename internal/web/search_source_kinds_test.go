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

// TestSearch_SubmitsSourceKindsFilter pins the headline contract:
// POST /search with source_kinds=gitlab,docbuilder must pass those
// kinds through to the service as SearchFilters.SourceKinds. The
// service-layer post-filter (applySourceKindFilters) then drops
// out-of-scope chunks before they reach the template. Without
// this wire-up, the search form's source-kind checkboxes have
// no effect on retrieval.
//
// This is the search-side equivalent of
// TestHandleChatMessage_ThreadsSourceKindsFilterToService — same
// per-source-kind UX idea, different endpoint. Search has no
// session (it's a stateless query) so the test doesn't exercise
// any sticky-default persistence; just the per-query filter
// round-trip.
func TestSearch_SubmitsSourceKindsFilter(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	fake := &fakeService{
		searchResults: nil, // empty — no results; the assertion is on filters, not content
	}
	s := NewServer(cfg, fake)

	cases := []struct {
		name            string
		form            string
		wantSourceKinds []models.SourceKind
	}{
		{
			name:            "no source_kinds form value -> empty filter (all sources)",
			form:            "query=test",
			wantSourceKinds: nil,
		},
		{
			name:            "single gitlab -> [SourceGitLab]",
			form:            "query=test&source_kinds=gitlab",
			wantSourceKinds: []models.SourceKind{models.SourceGitLab},
		},
		{
			name:            "two kinds -> both",
			form:            "query=test&source_kinds=gitlab,docbuilder",
			wantSourceKinds: []models.SourceKind{models.SourceGitLab, models.SourceDocbuilder},
		},
		{
			name:            "unknown kind in form -> dropped",
			form:            "query=test&source_kinds=gitlab,redmine",
			wantSourceKinds: []models.SourceKind{models.SourceGitLab},
		},
		{
			name:            "multi-checkbox syntax (curl source_kinds=gitlab&source_kinds=docbuilder)",
			form:            "query=test&source_kinds=gitlab&source_kinds=docbuilder",
			wantSourceKinds: []models.SourceKind{models.SourceGitLab, models.SourceDocbuilder},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, "/search",
				strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			s.router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code,
				"POST /search must return 200 (htmx-swappable results fragment)")
			require.Equal(t, tc.wantSourceKinds, fake.lastSearchFilters.SourceKinds,
				"the SearchFilters.SourceKinds passed to Service.Search must match what the form parsed")
		})
	}
}

// TestSearchForm_RendersSourceKindMultiSelect pins the headline
// UI contract: the search form renders one checkbox per known
// source kind, with both pre-checked on first visit (matching
// chat's "no default = all sources" UX). Search has no session
// (every query is fresh) but the operator-visible default state
// is still "all sources" — same as the chat form's empty-
// SelectedKinds branch.
func TestSearchForm_RendersSourceKindMultiSelect(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""
	fake := &fakeService{
		tags:       []string{"tut"},
		categories: []string{"Guides"},
	}
	s := NewServer(cfg, fake)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	require.Contains(t, body, "name=\"source_kinds\"",
		"the form must carry the source_kinds field")
	require.Contains(t, body, `value="gitlab"`,
		"the form must render a checkbox per known kind (gitlab)")
	require.Contains(t, body, `value="docbuilder"`,
		"the form must render a checkbox per known kind (docbuilder)")
	// First visit: both pre-checked (no default = all sources).
	require.True(t, isCheckedBody(body, "gitlab"),
		"first-visit search form must pre-check gitlab (no default = all sources)")
	require.True(t, isCheckedBody(body, "docbuilder"),
		"first-visit search form must pre-check docbuilder (no default = all sources)")
}

// isCheckedBody: helper to find whether a checkbox with the given
// value attribute is checked in the rendered body. Whitespace-
// tolerant (the go template emits `value="x"\n\tchecked>`).
func isCheckedBody(body, valueAttr string) bool {
	idx := strings.Index(body, `value="`+valueAttr+`"`)
	if idx == -1 {
		return false
	}
	closeIdx := strings.Index(body[idx:], ">")
	if closeIdx == -1 {
		return false
	}
	inner := body[idx : idx+closeIdx]
	return strings.Contains(inner, "checked")
}

// suppress unused import warning for url when test changes..
var _ = url.Values{}
