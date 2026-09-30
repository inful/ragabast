package models

import (
	"path/filepath"
	"strings"
)

// FilenameFromPath returns the basename of path with its
// extension stripped. Used by SearchResult.DisplayLabel to show a
// friendly filename when a cited document has no title.
//
// Edge cases:
//   - empty input returns "" so callers can detect "no path"
//   - paths that resolve to "." or "/" return ""
//   - paths whose basename is itself an extension (".md",
//     ".gitignore") are returned as-is — dotfiles are valid
//     identifiers to show
//
// The implementation uses filepath.Base + filepath.Ext so it
// handles platform separators portably (the ingest pipeline and
// the operator both see the same result).
func FilenameFromPath(path string) string {
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "" || base == "." || base == "/" || base == string(filepath.Separator) {
		return ""
	}
	ext := filepath.Ext(base)
	if ext == "" {
		return base
	}
	if ext == base {
		// Dotfile with no other extension ("/.gitignore").
		// Keep it visible rather than collapsing to "".
		return base
	}
	return strings.TrimSuffix(base, ext)
}

// DisplayLabel returns the human-readable label for a search
// result. The preference order is:
//
//  1. DocumentTitle (most user-friendly; set from the parent
//     document's H1 by the parser)
//  2. FilenameFromPath(DocumentFilePath) (the basename without
//     its extension — the on-disk filename the operator
//     recognizes)
//  3. DocumentID (the opaque UUID — the existing fallback)
//
// All three are trimmed of whitespace before the comparison so
// a DocumentTitle that's all whitespace doesn't accidentally win
// over a usable filename. The function is safe to call on a
// zero-value SearchResult; an empty result falls through to "".
//
// Callers that previously rendered "title or document_id" should
// use this method instead so the filename fallback is consistent
// everywhere.
func (s SearchResult) DisplayLabel() string {
	if t := strings.TrimSpace(s.DocumentTitle); t != "" {
		return t
	}
	if path := strings.TrimSpace(s.DocumentFilePath); path != "" {
		if name := FilenameFromPath(path); name != "" {
			return name
		}
	}
	return s.DocumentID
}
