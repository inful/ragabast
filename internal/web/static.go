package web

import (
	"embed"
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
var staticAssets = map[string]staticAsset{
	"bulma.min.css": {
		contentType: "text/css; charset=utf-8",
	},
	"htmx.min.js": {
		contentType: "application/javascript; charset=utf-8",
	},
}

// staticAsset is one entry in the staticAssets allow-list. The
// contentType is what we serve the file as; charset is explicit
// so the browser doesn't sniff and guess.
type staticAsset struct {
	contentType string
}
