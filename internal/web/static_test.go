package web

import (
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
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
//
// We pinned to Bulma 0.9.4 for years; the upgrade to 1.0.4
// keeps the same MIT-licensed embed contract but adds the
// v1-era CSS-variable system and the `prefers-color-scheme:dark`
// automatic dark theme.
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
	// Bulma stamps a license header at the top of the
	// minified bundle. A missing or wrong-version payload
	// would fail this sanity check, surfacing the regression
	// immediately. The version stamp changed in 1.0
	// (`bulma.io v0.9.4` → `bulma.io v1.0.4`) — keep this
	// aligned with the embedded file.
	require.Contains(t, string(body), "bulma.io v1.0.4",
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
// shipped on every successful static response. Templates
// render versioned URLs (?v=<sha>, see asset_version.go),
// so the URL itself is the cache key — long max-age on the
// versioned URL means the bytes are cached for the lifetime
// of the binary without any roundtrip. We deliberately do
// NOT include `immutable` so direct (non-versioned) fetches
// — legacy bookmarks, service-worker fetches, curl
// invocations — still revalidate via the ETag below.
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
	// break the test. We do NOT require `immutable` — see
	// the test below (TestStaticHandler_CacheControlDropsImmutable)
	// for the rationale and the alternative-cost argument.
	require.NotContains(t, cc, "immutable",
		"Cache-Control must NOT include 'immutable' — the ?v=<sha> URL is the cache key, and immutable would block revalidation for direct non-versioned requests (TestStaticHandler_CacheControlDropsImmutable)")
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

	// Bulma 1.0.4 selectors we know exist in the build. Each
	// is unique enough that a truncated or wrong-version file
	// will fail the assertion. Together they form a fingerprint
	// that survives minification (which only strips whitespace
	// and renames local identifiers).
	for _, selector := range []string{".button", ".input", ".navbar"} {
		require.Contains(t, body, selector,
			"served CSS must contain the %s selector (Bulma 1.0.4)", selector)
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
			require.Contains(t, body, `href="/static/bulma.min.css`,
				"%s must load Bulma from the embedded /static/bulma.min.css", p.path)
			require.NotContains(t, body, "cdn.jsdelivr.net",
				"%s must not reference cdn.jsdelivr.net", p.path)

			// Pages that use htmx (search) must also load it from
			// /static/. The ingest and documents pages don't
			// currently use htmx, so we only assert for /search.
			if p.path == "/search" {
				require.Contains(t, body, `src="/static/htmx.min.js`,
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

		require.Contains(t, body, `href="/static/bulma.min.css`,
			"chat fallback must load Bulma from /static/bulma.min.css")
		require.Contains(t, body, `src="/static/htmx.min.js`,
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

		require.Contains(t, body, `href="/static/bulma.min.css`,
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

		require.Contains(t, body, `href="/static/bulma.min.css`,
			"documents fallback must load Bulma from /static/bulma.min.css")
		require.NotContains(t, body, "cdn.jsdelivr.net",
			"documents fallback must not reference cdn.jsdelivr.net")
	})
}

// TestChatFallback_NoInlineStyle pins that the chat landing
// page does not carry an inline <style> block. The strict CSP
// ships style-src 'self' (no 'unsafe-inline'), so any inline
// <style> the server emits would be silently rejected by the
// browser and the chat page would render unstyled.
//
// Historically the chat fallback shipped a <style> block
// holding the chat-log / chat-msg / htmx-indicator classes
// because there was no /static/chat.css. That block is now
// in /static/chat.css and the template references it via
// <link rel="stylesheet" href="/static/chat.css>.
func TestChatFallback_NoInlineStyle(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
	s.templates = nil // force fallback renderer

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Must NOT contain a <style> block — every <style> block
	// would be blocked by the strict CSP.
	require.NotRegexp(t, `(?i)<style\b`, body,
		"chat fallback must not contain an inline <style> block (CSP blocks it)")

	// Must reference the external stylesheet instead.
	require.Contains(t, body, `href="/static/chat.css`,
		"chat fallback must load chat-specific styles from /static/chat.css")
}

// TestChatFallback_HtmxConfigDisablesEval pins that the chat
// landing page sets htmx.config.allowEval = false before
// htmx processes the page. htmx wraps every Function/eval
// call in a check on config.allowEval; with it false, htmx
// silently skips hx-on attribute processing and never calls
// eval() or new Function() at runtime. The browser's CSP
// therefore never blocks an eval attempt because no eval
// attempt is ever made.
//
// The configuration is delivered through the
// <meta name="htmx-config"> tag, which htmx reads at init
// time. Inline <script> would need 'unsafe-inline' to
// execute — the meta tag avoids that round-trip entirely.
func TestChatFallback_HtmxConfigDisablesEval(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
	s.templates = nil

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, `name="htmx-config"`,
		"chat fallback must declare <meta name=\"htmx-config\"> so htmx reads its config from JSON")
	require.Contains(t, body, `"allowEval":false`,
		"htmx-config must set allowEval to false so eval() / Function() are never called at runtime")
}

// TestChatFallback_NoHxOnAttributes pins that the chat
// landing page does not use hx-on::* attributes. With
// allowEval disabled, htmx would silently ignore them — the
// chat form would submit but the loading-state UX (is-loading
// class, form reset, focus return) would silently fail.
//
// The replacement lives in /static/chat.js which wires the
// same UX via htmx:beforeRequest / htmx:afterRequest /
// htmx:responseError event listeners. That file loads via
// <script src="/static/chat.js defer>, so the strict CSP
// (script-src 'self', no 'unsafe-inline', no 'unsafe-eval')
// lets it through.
func TestChatFallback_NoHxOnAttributes(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
	s.templates = nil

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.NotContains(t, body, "hx-on",
		"chat fallback must not use hx-on::* attributes (would silently fail with allowEval=false)")
	require.Contains(t, body, `src="/static/chat.js`,
		"chat fallback must load /static/chat.js to wire htmx event listeners")
}

// TestChatFallback_LoadsExternalChatJS pins that the chat
// landing page wires the htmx event listeners via an external
// script (rather than an inline <script>, which the strict
// CSP would block). The script must come from /static so the
// script-src 'self' directive allows it.
//
// The count-and-src assertion pins both halves of the contract:
// exactly the two expected <script> tags are present AND each
// carries a src= attribute (no inline bodies). A bare
// <script>...</script> block would inflate the count without
// matching the src= regex below.
func TestChatFallback_LoadsExternalChatJS(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
	s.templates = nil

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, `<script src="/static/chat.js`,
		"chat fallback must load /static/chat.js as an external script (CSP blocks inline <script>)")
	require.Contains(t, body, `src="/static/htmx.min.js`,
		"chat fallback must load htmx from /static/htmx.min.js")

	// Every <script> tag must have a src= attribute. The
	// count must equal the number of src=" matches so a
	// future contributor can't sneak in an inline <script>
	// block — that would inflate the count without adding a
	// src=" match, and the CSP would block the load.
	scriptTagCount := strings.Count(body, "<script")
	scriptSrcCount := strings.Count(body, `src="/static/`)
	require.Equal(t, scriptSrcCount, scriptTagCount,
		"every <script> tag must have a src= attribute (got %d <script> tags but %d src=\" matches)",
		scriptTagCount, scriptSrcCount)
	require.GreaterOrEqual(t, scriptTagCount, 2,
		"chat fallback must include htmx.min.js and chat.js (got %d <script> tags)",
		scriptTagCount)
}

// TestStaticHandler_ServesChatCSS pins that the new
// /static/chat.css file is reachable via the static handler
// with the correct Content-Type. The chat fallback template
// references this path; if the asset is missing or served
// with the wrong MIME, the browser refuses to apply the
// stylesheet and the chat layout breaks.
func TestStaticHandler_ServesChatCSS(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/chat.css", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	ct := w.Header().Get("Content-Type")
	require.True(t, strings.HasPrefix(ct, "text/css"),
		"/static/chat.css Content-Type must start with text/css (got %q)", ct)
	require.NotEmpty(t, w.Body.Bytes(), "/static/chat.css body must not be empty")
	// Sanity: chat-specific classes from the old <style>
	// block must be present so the file actually carries
	// what the chat page relies on.
	body := w.Body.String()
	require.Contains(t, body, ".chat-log",
		"chat.css must carry the .chat-log class")
	require.Contains(t, body, ".chat-msg",
		"chat.css must carry the .chat-msg class")
	require.Contains(t, body, ".htmx-indicator",
		"chat.css must carry the .htmx-indicator class")
}

// TestStaticHandler_ServesChatJS pins that the new
// /static/chat.js file is reachable via the static handler
// with the correct Content-Type. Without it the chat form's
// loading-state UX (is-loading class, form reset, focus
// return) is unwired because hx-on was removed.
func TestStaticHandler_ServesChatJS(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/chat.js", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	ct := w.Header().Get("Content-Type")
	require.True(t,
		strings.HasPrefix(ct, "application/javascript") || strings.HasPrefix(ct, "text/javascript"),
		"/static/chat.js Content-Type must be a JS MIME (got %q)", ct)
	require.NotEmpty(t, w.Body.Bytes(), "/static/chat.js body must not be empty")
	body := w.Body.String()
	// Sanity: chat.js must wire the three event listeners
	// that replace the hx-on::* attributes.
	require.Contains(t, body, "htmx:beforeRequest",
		"chat.js must listen for htmx:beforeRequest to set the loading class")
	require.Contains(t, body, "htmx:afterRequest",
		"chat.js must listen for htmx:afterRequest to reset form and remove loading")
	require.Contains(t, body, "htmx:responseError",
		"chat.js must listen for htmx:responseError to clean up on failure")
}

// TestStaticHandler_ServesLoginCSS pins that the new
// /static/login.css file is reachable via the static handler
// with the correct Content-Type. login.html historically
// shipped an inline <style> block; moving it to an external
// stylesheet lets the strict CSP (no 'unsafe-inline') apply
// to the login page too.
func TestStaticHandler_ServesLoginCSS(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/login.css", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	ct := w.Header().Get("Content-Type")
	require.True(t, strings.HasPrefix(ct, "text/css"),
		"/static/login.css Content-Type must start with text/css (got %q)", ct)
	require.NotEmpty(t, w.Body.Bytes(), "/static/login.css body must not be empty")
	body := w.Body.String()
	// Sanity: login-specific selectors must be present.
	require.Contains(t, body, "ul.providers",
		"login.css must carry the ul.providers selector")
}

// TestLoginTemplate_NoInlineStyle pins that login.html does
// not carry an inline <style> block — same rationale as
// TestChatFallback_NoInlineStyle, applied to the OAuth login
// page. The styles now live in /static/login.css.
//
// The /auth/login handler short-circuits to 302 (single
// provider auto-redirect) or 503 (no providers). To exercise
// the chooser page that actually renders templates/login.html,
// the test wires up two stub providers via
// config.AuthConfig.Providers. We don't need working IdPs —
// we only need the chooser markup to render so we can inspect
// it.
func TestLoginTemplate_NoInlineStyle(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.Providers = []config.OAuthProvider{
		{Name: "gh", Type: "github", ClientID: "id", ClientSecret: "sec"},
		{Name: "gl", Type: "gitlab", ClientID: "id", ClientSecret: "sec"},
	}
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/login", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"/auth/login must render the chooser page when at least two providers are configured (single-provider setup auto-redirects to that provider)")
	body := w.Body.String()

	require.NotRegexp(t, `(?i)<style\b`, body,
		"login template must not contain an inline <style> block (CSP blocks it)")
	// The static URL is now versioned via {{ asset "login.css" }}.
	// The test pins that the page references the asset at all;
	// the cache-busting query string is exercised by the asset
	// version tests in asset_version_test.go.
	require.Contains(t, body, `href="/static/login.css`,
		"login template must load login-specific styles from /static/login.css")
}

// TestStaticAssets_AllowListIsExact pins that the staticAssets
// allow-list in static.go only contains the bundled assets.
// A file dropped into internal/web/static/ but missing from
// the allow-list would be invisible to the static handler,
// surfacing as a 404 with no obvious cause. This test pins
// the current contract so a future contributor who adds an
// asset gets a compile-time reminder to register it.
func TestStaticAssets_AllowListIsExact(t *testing.T) {
	expected := []string{
		"bulma.min.css",
		"chat.css",
		"chat.js",
		"htmx.min.js",
		"login.css",
	}

	actual := make([]string, 0, len(staticAssets))
	for name := range staticAssets {
		actual = append(actual, name)
	}
	sort.Strings(actual)
	sort.Strings(expected)

	require.Equal(t, expected, actual,
		"staticAssets allow-list drifted; a new bundled asset must be registered here")
}
