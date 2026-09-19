package web

import "embed"

// templatesFS holds the HTML templates that ship with the binary,
// baked in at compile time. This is what makes the published
// Docker image self-contained: `docker run ghcr.io/inful/ragabast`
// works without a templates/ directory on disk.
//
// Operators can still override at runtime by setting
// `paths.templates_dir` (env: TEMPLATES_DIR); when set,
// NewServer falls back to a disk glob in preference to the
// embedded bundle. The override path is intended for shipping a
// custom template set without rebuilding the binary.
//
//go:embed templates/*.html
var templatesFS embed.FS
