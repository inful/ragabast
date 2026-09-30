package parser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDocument_GeneratesFingerprintAndStableID(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nfingerprint: \"auto-generated-if-empty\"\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Equal(t, "sample", doc.UID)
	require.NotEmpty(t, doc.Fingerprint)
	require.NotEmpty(t, doc.Fingerprint, "fingerprint should be populated even when not in frontmatter")
}

func TestParseDocument_PreservesExplicitFingerprintAndStableID(t *testing.T) {
	p := NewDocbuilderParser()

	fp := "b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9"
	raw := []byte("---\nfingerprint: " + fp + "\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Equal(t, "sample", doc.UID)
	require.Equal(t, fp, doc.Fingerprint)
}

func TestParseDocument_AllowsMissingURLs(t *testing.T) {
	p := NewDocbuilderParser()

	fp := "b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9"
	raw := []byte("---\nfingerprint: " + fp + "\nuid: sample\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Empty(t, doc.URLs)
}

// TestParseDocument_CRLFFrontmatter pins the headline fix for
// issue #82: docbuilder content pasted from Windows or any
// CRLF-emitting source (chat clients, browser textareas that
// preserve the user's paste) must produce the same parsed
// document as LF content. The browser submit path sends
// `---\r\nuid:...\r\n---\r\n`; before this fix the parser failed
// to recognize the opening delimiter and treated the whole
// input as markdown, leaving doc.UID empty and triggering the
// 500 "document UID is required" error on the web ingest path.
func TestParseDocument_CRLFFrontmatter(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\r\nuid: crlf-doc\r\nfingerprint: crlf-doc-v1\r\n---\r\n\r\n# Hello\r\n\r\nbody\r\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "crlf-doc", doc.UID)
	require.Equal(t, "crlf-doc", doc.ID)
	require.Equal(t, "crlf-doc-v1", doc.Fingerprint)
	require.Equal(t, "Hello", doc.Title,
		"H1 must extract correctly when the body uses CRLF line endings")
}

// TestParseDocument_CRLFPreservesRawContent ensures the fix
// normalizes CRLF only for parsing, not for the stored RawContent
// field. RawContent is what gets re-ingested later and what
// operators see in logs/diagnostics; rewriting it would hide the
// original bytes.
func TestParseDocument_CRLFPreservesRawContent(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\r\nuid: raw-cr-preserved\r\n---\r\nbody\r\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, raw, doc.RawContent,
		"RawContent must be the original bytes, not the normalized form")
}
