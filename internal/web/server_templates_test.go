package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestLoadTemplates_UsesEmbeddedWhenTemplatesDirEmpty pins the
// default-install path: cfg.Paths.TemplatesDir empty → embedded
// templates parsed → the named sub-template "search.html" (one of
// the canonical pages) is present and renders.
func TestLoadTemplates_UsesEmbeddedWhenTemplatesDirEmpty(t *testing.T) {
	s := &Server{config: config.DefaultConfig()}

	s.loadTemplates()

	require.NotNil(t, s.templates, "templates must be non-nil after load")
	tmpl := s.templates.Lookup("search.html")
	assert.NotNil(t, tmpl, "embedded search.html must be present")
}

// TestLoadTemplates_OperatorSuppliedDirParsed pins the
// operator-override path: when cfg.Paths.TemplatesDir points
// at a directory containing .html files, those files are
// parsed first. The subsequent re-parse with the asset
// FuncMap (which always reads from the embedded FS to bake
// in the `asset` function) overwrites the operator's
// templates — a known interaction of the current
// implementation. The test asserts the *current* behavior:
// the operator's templates were parsed (no error), and the
// embedded set is the final result.
//
// This is documented here so a future contributor who
// actually wants operator-supplied templates to win can
// find the test that pins the current (surprising) outcome
// and decide what to change. See plans/ for the
// TemplatesDir precedence note in the daisyUI migration
// plan.
func TestLoadTemplates_OperatorSuppliedDirParsed(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel.html")
	require.NoError(t, os.WriteFile(sentinel, []byte(`<p>operator-page</p>`), 0o600))

	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = dir

	s := &Server{config: cfg}
	s.loadTemplates()

	require.NotNil(t, s.templates, "templates must be non-nil after load")
	// Embedded search.html must be present (the re-parse
	// with FuncMap overwrites the operator's set with the
	// embedded set, which is the current behavior).
	tmpl := s.templates.Lookup("search.html")
	assert.NotNil(t, tmpl, "embedded search.html must be present after load with operator dir")
}

// TestLoadTemplates_FallsBackToEmbeddedWhenCustomDirFails pins
// the failure path: when cfg.Paths.TemplatesDir is set to a path
// that does not exist (or has no .html files), loadTemplates
// logs the failure and falls back to the embedded templates.
// Without the fall-through, an operator typo in TEMPLATES_DIR
// would take down every page.
func TestLoadTemplates_FallsBackToEmbeddedWhenCustomDirFails(t *testing.T) {
	dir := t.TempDir()
	// Make the directory empty: ParseGlob on "*.html" finds
	// nothing → returns an error. The fall-back must kick in.
	missing := filepath.Join(dir, "does-not-exist")
	// Sanity: the path really doesn't exist.
	_, statErr := os.Stat(missing)
	require.True(t, os.IsNotExist(statErr), "precondition: path must not exist")

	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = missing

	s := &Server{config: cfg}
	s.loadTemplates()

	require.NotNil(t, s.templates, "templates must be non-nil even when custom dir is unusable")
	tmpl := s.templates.Lookup("search.html")
	assert.NotNil(t, tmpl, "embedded search.html must be the fall-back result")
}

// TestLoadTemplates_AssetFuncResolvesInEmbeddedTemplates pins
// the contract: every embedded template that uses {{ asset ... }}
// must render without an "asset not defined" error. This is the
// reason for the re-parse via template.New("base").Funcs(...)
// inside loadTemplates — without it, the original ParseFS
// rejects the function and every page errors out.
func TestLoadTemplates_AssetFuncResolvesInEmbeddedTemplates(t *testing.T) {
	s := &Server{config: config.DefaultConfig()}
	s.loadTemplates()

	require.NotNil(t, s.templates, "templates must be non-nil")
	// Pick any embedded template; every one of them uses
	// {{ asset "..." }} for the CSS link. We pick search.html
	// because it is the canonical first page.
	tmpl := s.templates.Lookup("search.html")
	require.NotNil(t, tmpl, "embedded search.html must be present")

	var buf strings.Builder
	err := tmpl.Execute(&buf, map[string]any{
		"Title":     "test",
		"CsrfToken": "csrf",
		"SessionID": "sid",
		"Header":    map[string]any{},
	})
	require.NoError(t, err, "embedded template must execute without an asset-related error")
	assert.Contains(t, buf.String(), "/static/daisyui.min.css",
		"rendered HTML should reference the cache-busted CSS")
	assert.Contains(t, buf.String(), "?v=", "asset URL should carry the ?v= cache-bust query")
}

// TestLoadTemplates_HeaderBlockRegistered pins the navbar
// follow-on: the shared "header" template block is registered
// on the embedded set so {{ template "header" .Header }} in
// the page templates resolves. Without it, the navbar is
// missing from every embedded page.
func TestLoadTemplates_HeaderBlockRegistered(t *testing.T) {
	s := &Server{config: config.DefaultConfig()}
	s.loadTemplates()

	require.NotNil(t, s.templates)
	header := s.templates.Lookup("header")
	assert.NotNil(t, header, "header block must be registered on the embedded set")
}
