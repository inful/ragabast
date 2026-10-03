package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// staticFS holds the CSS / JS that ship inside the binary so
// the web UI does not have to reach a third-party CDN on every
// page load.
//
// Why this exists: the historical build loaded Bulma from
// https://cdn.jsdelivr.net and htmx from https://unpkg.com.
// That had three concrete downsides:
//
//  1. The operator's browser had to reach the CDN before the
//     page would render correctly. An air-gapped install or a
//     corporate network that blocked those hosts would render
//     unstyled, non-interactive HTML.
//  2. The CSP had to whitelist those origins in script-src and
//     style-src, widening the trusted-execution surface for
//     every visitor.
//  3. The build silently trusted the CDN to keep serving the
//     same bytes — a CDN compromise meant arbitrary JS ran in
//     the operator's origin.
//
// Embedding the assets removes all three concerns. The version
// is pinned at build time (the files under static/ are
// committed to the repo); a security update requires a rebuild,
// not a runtime fetch.
//
// License attribution (MIT for Bulma, BSD-2-Clause for htmx)
// lives in THIRD_PARTY_LICENSES.md at the repo root so any
// redistribution of the binary carries the required notices
// without the static handler having to serve the text.
//
//go:embed static/*
var staticFS embed.FS

// staticAssets is the curated allow-list of files the static
// handler will serve. We deliberately do NOT serve every file
// in staticFS via an open directory listing: a directory index
// would leak the names of every bundled asset and (worse) any
// file a future contributor accidentally drops into the
// directory. Adding a new bundled asset requires adding the
// filename here AND updating a template / fallback renderer to
// reference it.
//
// The current set is:
//
//   - bulma.min.css  — Bulma v1.0.4 (MIT). Loaded by every page.
//     Upgraded from 0.9.4 in the v1 migration: CSS variables,
//     `prefers-color-scheme:dark` automatic theme, no more Sass
//     dependency (the bundled CSS is what we ship — we don't
//     customize Sass variables at build time).
//
//   - htmx.min.js    — htmx v1.9.10 (BSD-2-Clause). Loaded by
//     the chat landing page and search.
//
//   - chat.css      — page-specific styles for the chat page.
//     Extracted from a former inline <style>
//     block so the strict CSP (no
//     'unsafe-inline') applies.
//
//   - chat.js       — wires the chat form's loading-state UX
//     via htmx event listeners. Replaces the
//     former hx-on::* attributes, which would
//     fail under the strict CSP because htmx
//     processes them with eval()/Function().
//
//   - login.css     — page-specific styles for the OAuth login
//     chooser. Extracted from a former inline
//     <style> block in templates/login.html.
//
// Bulma 1.x ships an automatic `@media (prefers-color-scheme:
// dark)` block in bulma.min.css, so OS-driven dark mode works
// with no extra CSS. The earlier manual override file
// (chat-dark.css) and the navbar theme-toggle button were
// removed — the override palette diverged from Bulma's
// designed dark scheme and was hard to maintain.
var staticAssets = map[string]staticAsset{
	"bulma.min.css": {
		contentType: "text/css; charset=utf-8",
	},
	"daisyui.min.css": {
		// Phase 1 of the Bulma -> DaisyUI migration (see
		// plans/daisyui-migration.md). Both files ship
		// side-by-side until Phase 3 drops Bulma. The
		// file is built by `make css` from
		// static/src/daisyui.css (Tailwind 4 + daisyUI 5)
		// and committed alongside its source. The size is
		// ~106 KB vs Bulma's 678 KB; the static test pins
		// a 250 KB ceiling as a regression guard.
		contentType: "text/css; charset=utf-8",
	},
	"htmx.min.js": {
		contentType: "application/javascript; charset=utf-8",
	},
	"chat.css": {
		contentType: "text/css; charset=utf-8",
	},
	"chat.js": {
		contentType: "application/javascript; charset=utf-8",
	},
	"login.css": {
		contentType: "text/css; charset=utf-8",
	},
}

// staticAsset is one entry in the staticAssets allow-list. The
// contentType is what we serve the file as; charset is explicit
// so the browser doesn't sniff and guess.
type staticAsset struct {
	contentType string
}

// handleStatic serves the bundled CSS / JS assets
// (Bulma, htmx) from the embedded staticFS. The route
// is registered with the chi pattern `/static/*`, so
// chi.URLParam(r, "*") holds everything after the
// prefix — e.g. `bulma.min.css`. The handler does NOT
// serve a directory listing: only filenames that appear
// in the staticAssets allow-list map above are
// reachable, so a future contributor who drops a file
// into internal/web/static/ by accident cannot expose
// it without also wiring it into a template.
//
// Defense in depth: path traversal cannot escape the
// embed.FS because embed.FS forbids `..` segments in
// fs.Open, and we look up filenames against the
// allow-list map rather than passing the raw URL path
// to fs.Open. The explicit list in TestStaticHandler_RejectsPathTraversal
// pins the safe behavior.
//
// Cache strategy: the assets are version-pinned at
// build time and cannot change without a rebuild, so we
// emit `Cache-Control: public, max-age=31536000,
// immutable` and a strong ETag derived from the file
// contents. The browser will skip revalidation on every
// page view, but a rebuild that swaps the bytes still
// invalidates caches (different ETag).
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// Chi's *-pattern delivers the suffix through the named
	// parameter "*"; an empty suffix means the client hit
	// `/static` without a filename, which is a 404 (no
	// directory listing).
	name := chi.URLParam(r, "*")
	if name == "" {
		http.NotFound(w, r)

		return
	}

	asset, ok := staticAssets[name]
	if !ok {
		http.NotFound(w, r)

		return
	}

	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		// The allow-list has the filename but the embed
		// doesn't — that is a build / repo corruption. We
		// refuse the request and log it; serving a 200 with
		// the wrong body would be worse than 500.
		internalError(w, r, "static asset missing from embed", err)

		return
	}

	// ETag derived from a cheap hash of the embedded bytes.
	// sha256 is overkill for an ETag but the cost is
	// negligible (one allocation per request) and avoids
	// importing yet another package for a single-purpose
	// hash. The ETag is quoted as required by RFC 7232 and
	// uses the weak-validator prefix (W/) only if we ever
	// return a transformed body — today the bytes are
	// served verbatim, so we use the strong form.
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	w.Header().Set("Content-Type", asset.contentType)
	// Cache-Control: long max-age for the within-binary-version
	// optimization; no `immutable` so direct (non-versioned)
	// requests — legacy bookmarks, service-worker fetches,
	// curl invocations — still revalidate via the ETag below.
	// Templates render versioned URLs (?v=<sha>, see
	// asset_version.go) so the versioned URL is the cache key;
	// within a single binary version the long max-age means
	// every page load reuses the bytes without a roundtrip.
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	w.Header().Set("ETag", etag)
	// Content-Length so the browser can skip the
	// transfer-encoding dance on a cache miss. Set
	// explicitly because we write raw bytes below rather
	// than using http.ServeContent.
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))

	// Conditional GET: a matching If-None-Match yields 304
	// with no body. Browsers send If-None-Match on every
	// request when the cached ETag is still fresh, so this
	// is the common path.
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		w.WriteHeader(http.StatusNotModified)

		return
	}

	if _, err := w.Write(data); err != nil {
		// Header is already on the response at this point;
		// best we can do is log so the operator sees the
		// dropped connection in the access log.
		log.Printf("server: static write %s %s: %v", r.Method, r.URL.Path, err)
	}
}

// etagMatches reports whether any of the ETags in an
// If-None-Match header value equals the response ETag.
//
// Per RFC 7232 §3.2 the header can carry a comma-separated
// list of opaque-tags and the wildcard "*". We treat any
// strong-equality match as a hit and the wildcard as a hit
// for any non-empty response. Weak-validator prefixes
// (`W/"…"`) are not produced by this server today, but the
// parser tolerates them so a future contributor adding weak
// validation does not need to revisit this helper.
func etagMatches(headerValue, etag string) bool {
	if headerValue == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(headerValue, ",") {
		candidate = strings.TrimSpace(candidate)
		// Strip the W/ weak prefix for comparison; we do not
		// distinguish strong from weak here because we do
		// not produce weak validators today.
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == etag {
			return true
		}
	}

	return false
}
