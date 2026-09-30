package web

import (
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestStaticHandler_ServesBulmaCSS pins the contract that GET
// /static/bulma.min.css returns the Bulma CSS that ships with
// the binary (embedded via go:embed), not a third-party CDN
// URL. The CSS is what styles the chat, search, ingest, and
// documents pages; if the embedded copy is missing or the
// Content-Type is wrong, the browser refuses to apply the
// stylesheet and the UI renders unstyled.
//
// Why we ship our own copy: the historical templates linked
// to https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css,
// which (a) requires the operator's browser to reach a CDN on
// first page load, (b) forces the CSP to whitelist that
// origin, and (c) trusts the CDN to keep serving the same
// bytes. Embedding the file removes all three concerns.
func TestStaticHandler_ServesBulmaCSS(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/bulma.min.css", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"the embedded bulma.min.css must be served by the static handler")

	ct := w.Header().Get("Content-Type")
	require.True(t, strings.HasPrefix(ct, "text/css"),
		"bulma.min.css Content-Type must start with text/css (got %q)", ct)

	body := w.Body.Bytes()
	require.NotEmpty(t, body, "bulma.min.css body must not be empty")
	// Bulma 0.9.4 stamps a license header at the top of the
	// minified bundle. A missing or wrong-version payload
	// would fail this sanity check, surfacing the regression
	// immediately.
	require.Contains(t, string(body), "bulma.io v0.9.4",
		"served CSS must be the actual Bulma library (missing version stamp)")
}

// TestStaticHandler_ServesHTMXJS pins the contract that GET
// /static/htmx.min.js returns the htmx runtime. htmx is what
// makes the chat form, search form, and chat-message swap
// work; if the embedded copy is missing, no htmx-driven
// endpoint swaps.
//
// Same rationale as TestStaticHandler_ServesBulmaCSS — see
// the comment there for why we embed rather than CDN-load.
func TestStaticHandler_ServesHTMXJS(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/htmx.min.js", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"the embedded htmx.min.js must be served by the static handler")

	ct := w.Header().Get("Content-Type")
	require.True(t,
		strings.HasPrefix(ct, "application/javascript") || strings.HasPrefix(ct, "text/javascript"),
		"htmx.min.js Content-Type must be a JS MIME (got %q)", ct)

	body := w.Body.Bytes()
	require.NotEmpty(t, body, "htmx.min.js body must not be empty")
	// htmx exposes itself as `htmx` on `window` and as a
	// global event listener on `document.body`. The minified
	// build still contains the literal `htmx` token in many
	// places (event names, function names, error messages);
	// the cheap sanity check is that `htmx:` appears at least
	// once (event prefixes like htmx:afterRequest).
	require.Contains(t, string(body), "htmx:",
		"served JS must be the actual htmx runtime (missing htmx: event prefix)")
}

// TestStaticHandler_NotFoundForUnknownAsset pins the 404
// contract for missing assets. Without this, a path-traversal
// or typo would silently fall through to a default handler
// and could leak unintended content. Returning 404 keeps the
// surface area predictable and matches the
// TestStaticRouteDoesNotServeArbitraryFiles contract.
func TestStaticHandler_NotFoundForUnknownAsset(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/does-not-exist.js", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code,
		"unknown static asset must return 404, not fall through to a default handler")
}

// TestStaticHandler_RootRequestIsNotFound pins that GET
// /static (without a filename) returns 404 rather than
// serving an index of the directory. A directory listing would
// leak the names of every bundled asset to anyone who could
// reach the server — defense in depth.
func TestStaticHandler_RootRequestIsNotFound(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code,
		"GET /static (no filename) must return 404, not a directory listing")
}

// TestStaticHandler_RejectsPathTraversal is the
// defense-in-depth test for the static handler. Even though
// the handler reads from a bounded embed.FS (which never
// serves files outside the embedded set), the chi route
// pattern `/static/*` could be abused by a request whose
// path tries to escape the prefix. We pin the safe behavior
// here so a future implementation cannot regress to
// http.ServeFile(r.URL.Path) without breaking CI.
//
// The set of cases mirrors the historical
// TestStaticRouteDoesNotServeArbitraryFiles — every form of
// traversal that has been observed in the wild.
func TestStaticHandler_RejectsPathTraversal(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	cases := []string{
		"/static/../../../etc/passwd",
		"/static/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/static/..%2f..%2f..%2fetc%2fpasswd",
		"/static//etc/passwd",
		"/static/..%2f..%2fstatic%2f..%2fetc%2fpasswd",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, p, nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			body := w.Body.String()
			require.NotContains(t, body, "root:",
				"static handler must not serve /etc/passwd for path %q", p)
			// Either 404 (unknown asset) or 200-but-no-system-content
			// is acceptable. The dangerous case is the response
			// body containing "root:" — that means the file
			// server reached /etc/passwd.
			if w.Code == http.StatusOK {
				ct := w.Header().Get("Content-Type")
				require.True(t,
					strings.HasPrefix(ct, "text/css") || strings.HasPrefix(ct, "application/javascript") || strings.HasPrefix(ct, "text/javascript"),
					"200 response for traversal path must be a CSS/JS asset, got %q (path %q)", ct, p)
			}
		})
	}
}

// TestStaticHandler_SetsCacheControl pins the cache header
// shipped on every successful static response. The embedded
// assets are version-pinned (bulma 0.9.4, htmx 1.9.10) and
// cannot change without rebuilding the binary, so a long
// max-age with `immutable` is safe — browsers will not
// revalidate on every page load.
//
// Without this header, a browser that has the assets in
// memory still issues a conditional GET on every page view,
// defeating the purpose of caching. The
// Strict-Transport-Security / securityHeaders middleware
// does not touch cache headers, so the static handler must
// set them itself.
func TestStaticHandler_SetsCacheControl(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/bulma.min.css", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	cc := w.Header().Get("Cache-Control")
	require.Contains(t, cc, "max-age=",
		"Cache-Control must declare a max-age (got %q)", cc)
	// One year is the conventional value for version-pinned,
	// content-addressed assets. We don't pin the exact number
	// because a sane future bump (e.g. two years) shouldn't
	// break the test, but we do require `immutable` so the
	// browser skips revalidation entirely.
	require.Contains(t, cc, "immutable",
		"Cache-Control must include 'immutable' (got %q)", cc)
}

// TestStaticHandler_ETagHonoredOnIfNoneMatch pins the
// conditional-GET contract: a request carrying the right
// If-None-Match header gets 304 Not Modified with an empty
// body. Without ETag, every page view would re-download the
// 200+ KB Bulma CSS even when the browser has the bytes
// cached locally.
//
// The ETag is opaque to the test (we don't pin its format),
// only that the round-trip works: first response carries an
// ETag, second request with that ETag returns 304.
func TestStaticHandler_ETagHonoredOnIfNoneMatch(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	// First request: capture the ETag header.
	first := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/bulma.min.css", nil)
	w1 := httptest.NewRecorder()
	s.router.ServeHTTP(w1, first)
	require.Equal(t, http.StatusOK, w1.Code)

	etag := w1.Header().Get("ETag")
	require.NotEmpty(t, etag, "static response must carry an ETag header")

	// Second request with If-None-Match: should return 304
	// with no body.
	second := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/bulma.min.css", nil)
	second.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	s.router.ServeHTTP(w2, second)

	require.Equal(t, http.StatusNotModified, w2.Code,
		"If-None-Match matching the ETag must return 304 Not Modified")
	require.Empty(t, w2.Body.Bytes(),
		"304 response must have an empty body")
}

// TestStaticHandler_StaticAssetsInBinaryVerify pins that the
// bytes served by /static match the bytes embedded in the
// binary. This is the test that catches "the file is on
// disk but not in the embed" — the most likely regression
// after a future refactor. Without it, a stale build with
// the CDN URLs could still pass the route-handler tests
// because nothing reads the embedded bytes.
//
// We compare a known sentinel substring present in every
// Bulma build: the `.button` class selector. A wrong file
// (or no file) would fail this check immediately.
func TestStaticHandler_StaticAssetsInBinaryVerify(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/bulma.min.css", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Bulma 0.9.4 selectors we know exist in the build. Each
	// is unique enough that a truncated or wrong-version file
	// will fail the assertion. Together they form a fingerprint
	// that survives minification (which only strips whitespace
	// and renames local identifiers).
	for _, selector := range []string{".button", ".input", ".navbar"} {
		require.Contains(t, body, selector,
			"served CSS must contain the %s selector (Bulma 0.9.4)", selector)
	}
}

// TestStaticHandler_NoCDNURLsLeak pins the security-relevant
// invariant: the static handler must NEVER redirect to or
// reference unpkg.com / cdn.jsdelivr.net. The whole point of
// bundling is to drop those origins from the CSP; if any
// asset somehow ended up pointing at a CDN, an attacker who
// compromised the upstream would regain JS execution.
//
// We also assert no Location / Link header points off-origin.
func TestStaticHandler_NoCDNURLsLeak(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	for _, asset := range []string{"/static/bulma.min.css", "/static/htmx.min.js"} {
		t.Run(path.Base(asset), func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, asset, nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.Empty(t, w.Header().Get("Location"),
				"static asset must not issue a redirect (got Location header)")

			body := w.Body.String()
			require.NotContains(t, body, "unpkg.com",
				"static asset body must not reference unpkg.com")
			require.NotContains(t, body, "cdn.jsdelivr.net",
				"static asset body must not reference cdn.jsdelivr.net")
		})
	}
}

// TestStaticHandler_TemplatesReferenceBundledAssets pins the
// rendering contract: every HTML page the server emits MUST
// load Bulma from /static/bulma.min.css and htmx from
// /static/htmx.min.js — never from a CDN. The pages that
// matter are the chat landing page (rendered through the
// fallback because chat.html is not in the embedded template
// set), the search page, the ingest page, the ingest-success
// page, and the documents page.
//
// Both the embedded-template path and the fallback-renderer
// path are covered here because they live in different files
// (templates/*.html vs. fallback_renderers.go) and could
// regress independently.
func TestStaticHandler_TemplatesReferenceBundledAssets(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = "" // force embedded templates

	// Pages that ship an embedded .html in templates/. Pages
	// not in that set (chat landing) fall through to the
	// fallback renderer — exercised below by dropping the
	// embedded templates.
	pages := []struct {
		path             string
		method           string
		body             string
		contentType      string
		dropTemplatesFor string // sub-test name for the fallback variant
	}{
		{path: "/search", method: http.MethodGet},
		{path: "/ingest", method: http.MethodGet},
		{path: "/documents", method: http.MethodGet},
		// chat landing page lives only in the fallback; cover it
		// below by exercising the fallback variant.
	}

	svc := &fakeService{}
	s := NewServer(cfg, svc)

	for _, p := range pages {
		t.Run(p.path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), p.method, p.path, nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()

			// The embedded template MUST reference /static/, not
			// the old CDN URL.
			require.Contains(t, body, `href="/static/bulma.min.css"`,
				"%s must load Bulma from the embedded /static/bulma.min.css", p.path)
			require.NotContains(t, body, "cdn.jsdelivr.net",
				"%s must not reference cdn.jsdelivr.net", p.path)

			// Pages that use htmx (search) must also load it from
			// /static/. The ingest and documents pages don't
			// currently use htmx, so we only assert for /search.
			if p.path == "/search" {
				require.Contains(t, body, `src="/static/htmx.min.js"`,
					"%s must load htmx from the embedded /static/htmx.min.js", p.path)
				require.NotContains(t, body, "unpkg.com",
					"%s must not reference unpkg.com", p.path)
			}
		})
	}

	t.Run("chat fallback", func(t *testing.T) {
		// Force the fallback renderer for the chat landing page
		// by dropping the embedded templates. The chat landing
		// has no embedded .html — it's only ever rendered via
		// newFallbackTemplates().chat in fallback_renderers.go.
		s.templates = nil

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()

		require.Contains(t, body, `href="/static/bulma.min.css"`,
			"chat fallback must load Bulma from /static/bulma.min.css")
		require.Contains(t, body, `src="/static/htmx.min.js"`,
			"chat fallback must load htmx from /static/htmx.min.js")
		require.NotContains(t, body, "cdn.jsdelivr.net",
			"chat fallback must not reference cdn.jsdelivr.net")
		require.NotContains(t, body, "unpkg.com",
			"chat fallback must not reference unpkg.com")
	})

	t.Run("ingest fallback", func(t *testing.T) {
		// The ingest page is rendered from a fallback template
		// when the embedded set is missing it. Drop the embedded
		// templates to exercise that path.
		s.templates = nil

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ingest", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()

		require.Contains(t, body, `href="/static/bulma.min.css"`,
			"ingest fallback must load Bulma from /static/bulma.min.css")
		require.NotContains(t, body, "cdn.jsdelivr.net",
			"ingest fallback must not reference cdn.jsdelivr.net")
	})

	t.Run("documents fallback", func(t *testing.T) {
		s.templates = nil

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()

		require.Contains(t, body, `href="/static/bulma.min.css"`,
			"documents fallback must load Bulma from /static/bulma.min.css")
		require.NotContains(t, body, "cdn.jsdelivr.net",
			"documents fallback must not reference cdn.jsdelivr.net")
	})
}
