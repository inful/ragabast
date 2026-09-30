package parser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitDocbuilderFrontmatter_HappyPath(t *testing.T) {
	raw := []byte("---\nfingerprint: abc\nuid: doc-1\n---\n# Hello\nbody\n")

	fm, md, ok := SplitDocbuilderFrontmatter(raw)

	require.True(t, ok)
	require.Equal(t, "fingerprint: abc\nuid: doc-1", string(fm))
	require.Equal(t, "# Hello\nbody", string(md))
}

func TestSplitDocbuilderFrontmatter_NoOpeningDelimiter(t *testing.T) {
	raw := []byte("# Just markdown\n")

	fm, md, ok := SplitDocbuilderFrontmatter(raw)

	require.False(t, ok, "no opening `---` means no frontmatter")
	require.Empty(t, fm)
	require.Equal(t, "# Just markdown", string(md), "full content is the markdown body")
}

func TestSplitDocbuilderFrontmatter_MissingClosingDelimiter(t *testing.T) {
	raw := []byte("---\nfingerprint: abc\nuid: doc-1\n# body without closer")

	_, md, ok := SplitDocbuilderFrontmatter(raw)

	require.False(t, ok, "missing closing `---` is treated as no frontmatter, not an error")
	require.Contains(t, string(md), "# body without closer",
		"lenient path returns the original content as markdown so callers can still parse it")
}

func TestSplitDocbuilderFrontmatter_TrimsSurroundingWhitespace(t *testing.T) {
	raw := []byte("  \n---\nfp: x\nuid: u\n---\n# body  \n")

	fm, md, ok := SplitDocbuilderFrontmatter(raw)

	require.True(t, ok)
	require.Equal(t, "fp: x\nuid: u", string(fm), "frontmatter interior trimmed")
	require.Equal(t, "# body", string(md), "markdown trimmed")
}

func TestSplitDocbuilderFrontmatter_EmptyInput(t *testing.T) {
	_, _, ok := SplitDocbuilderFrontmatter(nil)
	require.False(t, ok)

	_, _, ok = SplitDocbuilderFrontmatter([]byte("   \n"))
	require.False(t, ok)
}

// TestSplitDocbuilderFrontmatter_CRLFDelimiters pins the fix for
// issue #82: the lenient splitter must accept CRLF line endings
// in the `---` delimiters so content pasted from Windows editors
// or chat clients (which often emit CRLF) parses identically to LF.
// Without the fix, the closing `---\r\n` is never matched and the
// helper silently reports ok=false, which leaves the ingest path
// looking like the frontmatter was never there.
//
// The returned bytes are LF — the helper normalizes CRLF to LF
// at the top so the rest of the splitter stays LF-only.
func TestSplitDocbuilderFrontmatter_CRLFDelimiters(t *testing.T) {
	raw := []byte("---\r\nfingerprint: abc\r\nuid: doc-1\r\n---\r\n# Hello\r\nbody\r\n")

	fm, md, ok := SplitDocbuilderFrontmatter(raw)

	require.True(t, ok, "CRLF delimiters must be recognized just like LF")
	require.Equal(t, "fingerprint: abc\nuid: doc-1", string(fm),
		"frontmatter body normalizes to LF")
	require.Equal(t, "# Hello\nbody", string(md),
		"markdown body normalizes to LF")
}
