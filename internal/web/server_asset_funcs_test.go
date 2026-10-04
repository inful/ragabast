package web

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestInstallAssetFuncs_AssetFuncRegistered pins the headline
// contract of installAssetFuncs: after install, every template
// rendered through s.templates sees the `asset` function, and the
// returned URL carries the cache-busting query string the per-file
// SHA produces.
func TestInstallAssetFuncs_AssetFuncRegistered(t *testing.T) {
	s := &Server{
		config:        config.DefaultConfig(),
		assetVersions: buildAssetVersions(),
		fallback:      &fallbackTemplates{},
		templates:     template.New("base"),
	}

	s.installAssetFuncs()

	require.NotNil(t, s.templates, "templates must be non-nil after install")
	got, err := s.templates.New("probe.html").Parse(`{{ asset "daisyui.min.css" }}`)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, got.Execute(&buf, nil))

	out := buf.String()
	assert.True(t, strings.HasPrefix(out, "/static/daisyui.min.css"),
		"asset URL should start with /static/<name>, got %q", out)
	assert.Contains(t, out, "?v=", "asset URL should carry the cache-busting ?v= query, got %q", out)
}

// TestInstallAssetFuncs_FallbackChatAndDocumentsParsed pins the
// two Go-string fallback pages the embed-vs-disk path can fall
// back to. Both must be non-nil after install — a nil value here
// would render a 500 the first time a request hits /chat or
// /documents with no embedded template.
func TestInstallAssetFuncs_FallbackChatAndDocumentsParsed(t *testing.T) {
	s := &Server{
		config:        config.DefaultConfig(),
		assetVersions: buildAssetVersions(),
		fallback:      &fallbackTemplates{},
		templates:     template.New("base"),
	}

	s.installAssetFuncs()

	assert.NotNil(t, s.fallback.chat, "chat fallback must be non-nil after install")
	assert.NotNil(t, s.fallback.documents, "documents fallback must be non-nil after install")
}

// TestInstallAssetFuncs_UnknownAssetNameReturnsUnversionedURL
// pins the typo-tolerance: an `{{ asset "bogus.css" }}` call
// must not crash the page; it returns the unversioned URL so the
// static handler's permissive fallback (also returns the
// unversioned file) picks it up.
func TestInstallAssetFuncs_UnknownAssetNameReturnsUnversionedURL(t *testing.T) {
	s := &Server{
		config:        config.DefaultConfig(),
		assetVersions: buildAssetVersions(),
		fallback:      &fallbackTemplates{},
		templates:     template.New("base"),
	}

	s.installAssetFuncs()

	got, err := s.templates.New("probe.html").Parse(`{{ asset "no-such-asset.css" }}`)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, got.Execute(&buf, nil))

	assert.Equal(t, "/static/no-such-asset.css", buf.String(),
		"unknown asset name must return the unversioned URL")
}

// TestInstallAssetFuncs_AssetURLIsDeterministic pins the
// cache-busting contract: two calls with the same name produce
// the same URL. If assetVersions weren't keyed by name (or the
// FuncMap closure captured a non-deterministic value), the URL
// would differ between calls and the browser would see a "new"
// URL on every render — defeating the cache.
func TestInstallAssetFuncs_AssetURLIsDeterministic(t *testing.T) {
	s := &Server{
		config:        config.DefaultConfig(),
		assetVersions: buildAssetVersions(),
		fallback:      &fallbackTemplates{},
		templates:     template.New("base"),
	}

	s.installAssetFuncs()

	got, err := s.templates.New("probe.html").Parse(`{{ asset "daisyui.min.css" }}|{{ asset "daisyui.min.css" }}`)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, got.Execute(&buf, nil))

	parts := strings.Split(buf.String(), "|")
	require.Len(t, parts, 2)
	assert.Equal(t, parts[0], parts[1], "two calls to {{ asset <name> }} must produce the same URL")
}
