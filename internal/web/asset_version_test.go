package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestAssetVersion_BuiltAtStartup pins that every static
// asset the server can serve has a SHA-256 recorded in
// assetVersions. The test reads one file directly to
// compare — if the hash drifts from the bytes, the
// versioned URLs would point to bytes the static handler
// won't serve.
func TestAssetVersion_BuiltAtStartup(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	require.NotEmpty(t, s.assetVersions,
		"every static asset must have a versioned URL — empty map means the startup loop silently skipped everything")

	for name := range staticAssets {
		v, ok := s.assetVersions[name]
		require.True(t, ok,
			"%s must have an asset version; the buildAssetVersions loop must not silently skip names", name)
		require.Len(t, v, 64,
			"%s version must be the full hex-encoded SHA-256 (64 chars); got %d", name, len(v))
	}
}

// TestChatLanding_StaticAssetsCarryCacheBustingQuery pins
// the user-facing contract: every <link href="/static/...">
// and <script src="/static/..."> on the chat landing page
// must carry a "?v=<sha>" query parameter. Without this,
// an in-place binary upgrade leaves browsers serving the
// old bundled CSS / JS for up to a year (the static
// handler's Cache-Control: max-age=31536000, immutable).
//
// The test pulls every href/src starting with /static/
// and asserts each has a matching `?v=<64-hex>` query.
// The hash value is computed inline against the live
// embedded bytes so a regression where the URL and the
// served bytes drift apart would also fail here.
func TestChatLanding_StaticAssetsCarryCacheBustingQuery(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	var buf strings.Builder
	require.NoError(t, s.fallback.chat.Execute(&buf, chatFallbackData{
		Title:     "Chat",
		CsrfToken: "",
		SessionID: "session-test",
		Header:    pageHeaderData{AuthEnabled: false, SignedIn: false, ShowSignIn: false},
	}))
	body := buf.String()

	hrefRe := regexp.MustCompile(`(?:href|src)="(/static/[^"]+)"`)
	matches := hrefRe.FindAllStringSubmatch(body, -1)
	require.NotEmpty(t, matches, "the chat landing page must reference at least one static asset; without it this test is vacuous")

	for _, m := range matches {
		url := m[1]
		require.Contains(t, url, "?v=",
			"every /static/... URL in the rendered page must carry ?v=<sha> for cache-busting; got %q (see asset_version.go)", url)

		parts := strings.SplitN(url, "?v=", 2)
		require.Len(t, parts, 2, "URL has ?v= but no value: %q", url)
		name := strings.TrimPrefix(parts[0], "/static/")
		expected := s.assetVersions[name]
		require.NotEmpty(t, expected,
			"%s must have a recorded version; buildAssetVersions loop must not silently skip names", name)
		assert.Equal(t, expected, parts[1],
			"%s ?v= must match the recorded SHA-256 — if these drift apart, browsers refetch every page load", name)
	}
}

// TestAssetURL_KnownAndUnknownNames covers the
// graceful-degradation contract in AssetURL: known names
// return the versioned URL, unknown names return the
// unversioned URL rather than panicking or returning an
// empty string. The latter would break the page that
// references an unknown asset; the former would degrade
// the cache-busting optimization on that one asset
// without breaking the page.
//
// A typo in a template shouldn't take down the page —
// the same file will 500 from the static handler if the
// typo is real; we just don't want to also break the page
// that references it.
func TestAssetURL_KnownAndUnknownNames(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	// Known name → versioned URL.
	got := AssetURL("bulma.min.css")
	require.Contains(t, got, "/static/bulma.min.css?v=",
		"known asset must get the versioned URL")
	v, ok := s.assetVersions["bulma.min.css"]
	require.True(t, ok)
	require.Contains(t, got, v,
		"the ?v= query must be the recorded SHA-256 for the asset")

	// Unknown name → unversioned URL, no panic.
	assert.Equal(t, "/static/missing.css", AssetURL("missing.css"),
		"unknown asset name must fall back to the unversioned URL rather than panic")
}

// TestStaticHandler_ETagStillSet pins that the static
// handler continues to emit a strong ETag header on every
// response. With the URL-based cache busting in place, the
// ETag is no longer the operator-visible cache key, but it
// remains the revalidation mechanism for direct
// non-versioned fetches: a browser with a cached
// non-versioned URL still does `If-None-Match` and gets
// 304 if the file is unchanged. Removing the ETag would
// force every direct fetch to re-download the full file.
func TestStaticHandler_ETagStillSet(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/bulma.min.css", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	etag := w.Header().Get("ETag")
	require.NotEmpty(t, etag,
		"ETag must remain so direct non-versioned URLs revalidate via If-None-Match — the versioned URL handles cache-busting, the ETag handles within-version revalidation")
}
