package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestPageChrome_NoRedundantNavOnFallbackPages pins the
// contract that the always-on navbar (issue #85) is the
// primary navigation surface. Per-page "Back" links were
// carried over from the pre-navbar era and became duplicate
// navigation once the navbar was added — see the review at
// .review-screenshots/2026-09-30-ragabast-ui-review.
//
// This test exercises the FALLBACK renderer (s.templates = nil)
// for /documents, which historically emitted a "Back" anchor
// below the H1. The /search page is rendered only from an
// embedded template, so it has its own test below.
//
// PR 2 removed the HTML /ingest form, so /ingest is no
// longer a fallback-renderer page.
func TestPageChrome_NoRedundantNavOnFallbackPages(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	pages := []string{"/documents"}

	for _, path := range pages {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()

			// No "Back" anchor inside the page body. The
			// navbar still has the home link (covered by
			// TestFallbackHeader_NavLinksPresentOnEveryPage);
			// this pins that no page *also* renders a Back
			// link outside the navbar.
			assert.NotContains(t, body, `>Back<`,
				"%s must not render a duplicate Back link outside the navbar", path)
		})
	}
}

// TestSearchTemplate_NoRedundantNav pins the embedded
// search.html template against the same contract: no
// "Back" button and no Bulma breadcrumb duplicating the
// navbar. The /search route is rendered only from the
// embedded template (no fallback renderer exists), so the
// fallback-renderer test above cannot cover it; this test
// reads the template source via the host filesystem.
//
// Once the templates are reachable via an embed.FS the
// test could switch to that for the no-build-path
// guarantee; the file-read approach works today and is
// what every other template-rendering test in this
// package uses.
func TestSearchTemplate_NoRedundantNav(t *testing.T) {
	const rel = "templates/search.html"
	tmpl, err := os.ReadFile(rel)
	require.NoError(t, err, "search template must be readable from the working tree")
	require.NotEmpty(t, tmpl, "search template must not be empty")

	assert.NotContains(t, string(tmpl), `>Back<`,
		"search template must not render a duplicate Back button (the navbar covers it)")
	assert.NotContains(t, string(tmpl), `aria-label="breadcrumbs"`,
		"search template must not render a breadcrumb that duplicates the navbar links")
}
