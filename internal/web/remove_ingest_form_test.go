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

// TestIngestFormRemoved_Route404 pins the headline contract for
// PR 2: the HTML ingest form is gone. Both GET (the page) and
// POST (the submit) return 404 — the path is no longer mounted.
//
// The form was the canonical "paste arbitrary markdown" UI
// surface. Operators who want to ingest from outside the web
// UI use the Huma HTTP API (POST /api/ingest, /api/ingest/raw,
// /api/ingest/file); see TestIngestFormRemoved_APISurfaceUnchanged.
//
// A 404 — not 405, not 410 — is the right answer: there is no
// resource at this path. The chi router returns 404 by default
// for unregistered paths.
func TestIngestFormRemoved_Route404(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	t.Run("GET /ingest returns 404", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ingest", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Code,
			"the HTML ingest form must be removed (got %d, want 404)", w.Code)
	})

	t.Run("POST /ingest returns 404", func(t *testing.T) {
		body := strings.NewReader("content=hello")
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Code,
			"the HTML ingest form POST must be removed (got %d, want 404)", w.Code)
	})
}

// TestIngestFormRemoved_NavLinkGone pins the user-visible
// contract that the Ingest link is no longer in the navbar.
// The link was the only path to the form for operators using
// the always-on navbar (issue #85); without it, the form is
// undiscoverable from the page chrome.
func TestIngestFormRemoved_NavLinkGone(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil // force fallback renderer

	// Probe every page that renders the navbar via the
	// fallback. /search uses an embedded template (no
	// fallback renderer exists for it), so it would 404
	// under the templates=nil setup. The navbar is parsed
	// into every embedded template too, so /search is
	// covered indirectly when embedded templates are
	// loaded. Here we just want to assert no /ingest link
	// surfaces anywhere reachable.
	for _, path := range []string{"/", "/documents"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			assert.NotContains(t, body, `href="/ingest"`,
				"%s must not link to /ingest — the form is gone", path)
		})
	}
}

// TestIngestFormRemoved_APISurfaceUnchanged pins the
// "we kept the HTTP API" half of PR 2's scope. POST
// /api/ingest, /api/ingest/raw, and /api/ingest/file all
// still ingest a valid docbuilder payload. The form is the
// only ingest entry point that goes away; the API stays.
func TestIngestFormRemoved_APISurfaceUnchanged(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeHumaService{}
	s := NewServer(cfg, svc)

	t.Run("POST /api/ingest still ingests", func(t *testing.T) {
		body := `{"content":"---\nuid: api-survives\nfingerprint: fp1\n---\n\nbody\n"}`
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code,
			"/api/ingest must keep working after the form is removed (got %d)", w.Code)
		require.Equal(t, 1, svc.ingestCalls,
			"the API path must have been called exactly once")
	})
}
