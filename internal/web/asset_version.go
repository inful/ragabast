// Package web serves the ragabast HTTP surface — Huma-mounted
// /api routes, the fallback Go-string templates for every
// HTML page, the static-asset pipeline (with per-file
// cache-busting), and the OAuth / chat / search / ingest
// / documents pages.
//
// asset_version.go in particular backs the cache-busting
// query string (`?v=<sha>`) the templates embed in every
// <link href="/static/..."> and <script src="/static/...">.
// It defeats the browser's `Cache-Control: immutable +
// max-age=1y` cache across binary upgrades: a SHA that
// matches the bytes makes the URL the cache key, so an
// operator replacing the binary in place forces a
// refetch instead of serving stale assets for up to a
// year.
//
// Problem this solves:
//
//	The static handler emits Cache-Control:
//	  public, max-age=31536000, immutable
//	plus a strong ETag derived from the file content. The
//	ETag would let the browser revalidate on cache miss,
//	but `immutable` tells the browser to skip revalidation
//	entirely until max-age expires. So when an operator
//	replaces the binary in place (v0.11.2 → v0.11.3), the
//	browser keeps serving v0.11.2's daisyui.min.css for up to
//	a year because the URL hasn't changed.
//
//	Templates that render the page put the asset URL in an
//	href/src. If the URL itself changes between binaries,
//	the browser sees a fresh resource — no cache. The
//	per-file SHA is the natural cache key here: same bytes
//	across binaries → same SHA → same URL → cache hits;
//	different bytes → different SHA → different URL →
//	cache miss. We're already computing the SHA for the
//	ETag; this file reuses that computation to also bake it
//	into the URL.
//
// Where the value is computed:
//
//	Server.assetVersions, populated once at startup by
//	buildAssetVersions (called from NewServer). The map is
//	also mirrored to the package-level assetVersionsMap
//	so templates parsed at package init (notably the
//	OAuth login template in oauth.go) can read it through
//	the package-level AssetURL helper.
//
// Why full SHA-256 (64 chars) and not a truncated hash:
//
//   - The hash is the URL cache key. A collision here means
//     a stale asset ships to one browser in 2^N attempts.
//     SHA-256's 256-bit output is overkill; even 64 bits
//     (16 hex chars) is functionally safe for a few assets.
//     Using the same 64-char digest as the ETag means one
//     computation path for both.
//   - The URL is ugly but HTTP/2 doesn't care, CDNs don't
//     care, and no real operator grep-views URLs.
//   - The actual bytes are 32 (binary) or 64 (hex). Smaller
//     than a typical CSRF token.
//
// URL shape:
//
//	/static/<name>?v=<64-hex-of-SHA-256-of-content>
//
// The static handler ignores query strings (it routes on
// the path after /static/), so the same byte stream is
// served regardless of the version — the query string is
// pure cache-busting decoration.
package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sync"
)

// assetVersionsMap holds the global filename → SHA-256
// mirror. The package-level AssetURL helper reads from it so
// templates parsed at package init (notably the OAuth login
// chooser) can render versioned URLs without going through
// a Server instance.
//
// Populated exactly once via sync.Once in ensureAssetVersions
// so the init-order is irrelevant: callers don't care
// whether buildAssetVersions ran in NewServer or earlier.
var (
	assetVersionsMu  sync.RWMutex
	assetVersionsMap map[string]string
)

func ensureAssetVersions() map[string]string {
	assetVersionsMu.RLock()
	if assetVersionsMap != nil {
		defer assetVersionsMu.RUnlock()
		return assetVersionsMap
	}
	assetVersionsMu.RUnlock()

	assetVersionsMu.Lock()
	defer assetVersionsMu.Unlock()
	if assetVersionsMap != nil {
		return assetVersionsMap
	}
	assetVersionsMap = buildAssetVersions()
	return assetVersionsMap
}

// buildAssetVersions hashes every file in the embedded
// static bundle and returns a filename → hex-sha256 map.
// Called once at server construction; the result lives on
// Server.assetVersions for the lifetime of the process.
//
// The function reads each file via the embedded FS rather
// than computing the hash from a separate bytes source so
// the cache-busting value is guaranteed to match the bytes
// the static handler will actually serve — a mismatch
// would be a silent cache-miss-every-time bug.
func buildAssetVersions() map[string]string {
	out := make(map[string]string, len(staticAssets))
	for name := range staticAssets {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			// The allow-list (staticAssets) is hand-curated
			// alongside the embed. If a name is in the allow-list
			// but missing from the embed, that's a build / repo
			// corruption — skip rather than panic so a broken
			// build still serves the page (with a missing
			// cache-busting query) instead of crashing on startup.
			//
			// The static handler enforces the same invariant
			// at request time (handleStatic returns 500 if
			// ReadFile fails); this startup loop just doesn't
			// get to add a "?v=" to a URL the handler would 500 on.
			continue
		}
		sum := sha256.Sum256(data)
		out[name] = hex.EncodeToString(sum[:])
	}
	return out
}

// AssetURL is the package-level versioned-URL helper. It
// exists so templates parsed before NewServer runs (the
// login chooser template in oauth.go) can render versioned
// asset URLs without holding a *Server reference.
//
// Calls defer to ensureAssetVersions, which populates the
// map on first use; callers don't need to know whether the
// map was populated by NewServer or by their own call.
//
// Unknown names return the unversioned URL — the fallback
// is intentionally permissive (rather than an empty string
// or a panic) so a typo in a template can't take down the
// page. The static handler enforces the same permissive
// fallback at request time.
func AssetURL(name string) string {
	v := ensureAssetVersions()[name]
	if v == "" {
		return "/static/" + name
	}
	return "/static/" + name + "?v=" + url.QueryEscape(v)
}

// assetURL is the *Server method that NewServer wires into
// the embedded-template FuncMap. It returns the same value
// as AssetURL but reads from the per-Server map (which is
// populated alongside the package map at startup).
//
// Why a method that wraps AssetURL:
//
//	s.assetVersions is the per-Server mirror of the package
//	map — populated at NewServer construction from the
//	embedded FS. The method form gives templates a stable
//	receiver (s.assetURL) that always reads the Server's
//	view, which is the same data as AssetURL reads (the
//	buildAssetVersions output is mirrored to both).
//	Keeping the method lets us add Server-specific
//	overrides later (e.g. a per-tenant asset prefix)
//	without touching the FuncMap signature.
func (s *Server) assetURL(name string) string {
	v, ok := s.assetVersions[name]
	if !ok {
		return "/static/" + name
	}
	return "/static/" + name + "?v=" + url.QueryEscape(v)
}
