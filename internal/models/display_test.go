package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFilenameFromPath_HappyPath pins the headline behavior:
// a typical "dir/file.ext" returns the bare basename without the
// extension. This is the fallback we want for citation links when
// a document has no title.
func TestFilenameFromPath_HappyPath(t *testing.T) {
	require.Equal(t, "my-doc", FilenameFromPath("/var/docs/my-doc.md"))
	require.Equal(t, "my-doc", FilenameFromPath("my-doc.md"))
	require.Equal(t, "my-doc", FilenameFromPath("./my-doc.md"))
}

// TestFilenameFromPath_NoExtension returns the basename unchanged
// when there's nothing to trim. Important for "web_upload" and
// similar placeholders the ingest pipeline uses for documents that
// were never read from disk.
func TestFilenameFromPath_NoExtension(t *testing.T) {
	require.Equal(t, "web_upload", FilenameFromPath("web_upload"))
	require.Equal(t, "README", FilenameFromPath("README"))
}

// TestFilenameFromPath_EmptyPath returns empty so callers can
// detect "no usable file path" without sentinel values.
func TestFilenameFromPath_EmptyPath(t *testing.T) {
	require.Empty(t, FilenameFromPath(""))
}

// TestFilenameFromPath_Dotfile pins the case where the basename
// starts with a dot ("hidden" files). filepath.Ext treats the whole
// name as the extension; we keep the dot rather than returning an
// empty string, because ".gitignore" is a perfectly readable label.
func TestFilenameFromPath_Dotfile(t *testing.T) {
	require.Equal(t, ".gitignore", FilenameFromPath(".gitignore"))
	require.Equal(t, ".gitignore", FilenameFromPath("/path/.gitignore"))
}

// TestFilenameFromPath_OnlyExtension pins the behavior for
// paths that ARE their own extension (".md", ".gitignore"). We
// return them as-is because dotfiles are valid identifiers to
// surface and there is no stem to strip.
func TestFilenameFromPath_OnlyExtension(t *testing.T) {
	require.Equal(t, ".md", FilenameFromPath(".md"))
	require.Equal(t, ".gitignore", FilenameFromPath(".gitignore"))
}

// TestFilenameFromPath_MultiDot keeps the dots that aren't the
// final extension. "config.local.json" → "config.local".
func TestFilenameFromPath_MultiDot(t *testing.T) {
	require.Equal(t, "config.local", FilenameFromPath("/etc/config.local.json"))
}

// TestFilenameFromPath_TrailingSlash returns the basename as
// filepath.Base computes it (the trailing separator is consumed).
// The "/" root case is special-cased to return empty.
func TestFilenameFromPath_TrailingSlash(t *testing.T) {
	require.Equal(t, "docs", FilenameFromPath("/var/docs/"))
	require.Empty(t, FilenameFromPath("/"))
}

// TestSearchResult_DisplayLabel_TitleWins pins the headline
// behavior: when the document has a title, that's what the UI
// shows. This is unchanged from the existing behavior; the new
// helper just makes it consistent across inline-link text, the
// chat sources panel, and the search results panel.
func TestSearchResult_DisplayLabel_TitleWins(t *testing.T) {
	s := SearchResult{
		DocumentTitle:    "ADR 001",
		DocumentID:       "doc-1",
		DocumentFilePath: "/var/docs/adr-001.md",
	}
	require.Equal(t, "ADR 001", s.DisplayLabel())
}

// TestSearchResult_DisplayLabel_FallsBackToFilename pins the
// fix for this issue: a document with no title shows its filename
// (without extension) rather than the opaque document_id.
func TestSearchResult_DisplayLabel_FallsBackToFilename(t *testing.T) {
	s := SearchResult{
		DocumentID:       "doc-1",
		DocumentFilePath: "/var/docs/adr-001.md",
	}
	require.Equal(t, "adr-001", s.DisplayLabel(),
		"a document with no title must fall back to its filename")
}

// TestSearchResult_DisplayLabel_FallsBackToID pins the existing
// behavior preserved when neither title nor file_path is set —
// that's the case for legacy ingested chunks written before the
// file_path metadata was added.
func TestSearchResult_DisplayLabel_FallsBackToID(t *testing.T) {
	s := SearchResult{
		DocumentID: "doc-1",
	}
	require.Equal(t, "doc-1", s.DisplayLabel(),
		"with no title and no file_path, fall back to DocumentID")
}

// TestSearchResult_DisplayLabel_TrimsWhitespace mirrors
// sourceLinkText's strings.TrimSpace: a title that's all
// whitespace is treated as empty so the fallback chain still
// produces something useful.
func TestSearchResult_DisplayLabel_TrimsWhitespace(t *testing.T) {
	s := SearchResult{
		DocumentTitle:    "   \t\n",
		DocumentID:       "doc-1",
		DocumentFilePath: "/var/docs/adr-001.md",
	}
	require.Equal(t, "adr-001", s.DisplayLabel())
}

// TestSearchResult_DisplayLabel_WhitespaceFilename confirms
// trimming applies to file paths too (a path that resolves to
// whitespace-only is treated as missing).
func TestSearchResult_DisplayLabel_WhitespaceFilename(t *testing.T) {
	s := SearchResult{
		DocumentID:       "doc-1",
		DocumentFilePath: "   ",
	}
	require.Equal(t, "doc-1", s.DisplayLabel())
}

// TestDocumentInfo_DisplayLabel_TitleWins mirrors the SearchResult
// test for the /documents list page. Title still wins.
func TestDocumentInfo_DisplayLabel_TitleWins(t *testing.T) {
	d := DocumentInfo{Title: "ADR 001", UID: "adr-001", FilePath: "/var/docs/adr-001.md"}
	require.Equal(t, "ADR 001", d.DisplayLabel())
}

// TestDocumentInfo_DisplayLabel_FallsBackToFilename pins the fix
// for issue #84: when /documents surfaces a doc with no title,
// the row must show the filename (basename without extension),
// not the opaque UID.
func TestDocumentInfo_DisplayLabel_FallsBackToFilename(t *testing.T) {
	d := DocumentInfo{UID: "doc-without-title", FilePath: "/tmp/data/documents/untitled-ramble.md"}
	require.Equal(t, "untitled-ramble", d.DisplayLabel())
}

// TestDocumentInfo_DisplayLabel_FallsBackToID pins the legacy
// behavior preserved when neither title nor file_path is set —
// that's the case for chunks written before file_path was added
// to the chunk metadata.
func TestDocumentInfo_DisplayLabel_FallsBackToID(t *testing.T) {
	d := DocumentInfo{UID: "legacy-doc"}
	require.Equal(t, "legacy-doc", d.DisplayLabel())
}
