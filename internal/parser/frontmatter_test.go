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
