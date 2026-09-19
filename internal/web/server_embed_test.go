package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestServer_EmbeddedTemplates_RenderWithoutDiskDir pins the
// embed contract: with TemplatesDir explicitly empty, the
// server must serve /search from the embedded template bundle
// even when the working directory has no templates/ subdir.
//
// This is the property that makes the published Docker image
// self-contained: the binary embeds the templates at compile
// time and ships them in the executable. Without it, every
// published release would be a web-UI 404 factory because the
// container has no templates/ on disk.
//
// The test deliberately does NOT call chdirToRepoRoot: if the
// templates were loaded from disk, the test would fail because
// the test binary runs from internal/web/ where there is no
// templates/ directory.
func TestServer_EmbeddedTemplates_RenderWithoutDiskDir(t *testing.T) {
	// Stay in the package's test working directory (internal/web).
	// The embedded template loader should not need to read from
	// disk under any CWD.

	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = "" // force the embedded path

	s := NewServer(cfg, &fakeService{
		tags:       []string{"tut"},
		categories: []string{"Guides"},
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "GET /search must return 200 from the embedded templates")
	require.Contains(t, w.Body.String(), `<option value="tut"`,
		"the embedded template must render the available tags as <option>s")
}

// TestServer_EmbeddedTemplates_POST_RendersResults pins the same
// embed property for the POST /search path. Catches a regression
// where /search.html works but /search_results.html is still
// loaded from disk.
func TestServer_EmbeddedTemplates_POST_RendersResults(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""

	s := NewServer(cfg, &fakeService{
		searchResults: nil, // exercise the "no results" notification
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader("query=anything"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "No results found",
		"the embedded search_results template must render the empty-state notification")
}
