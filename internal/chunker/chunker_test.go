package chunker

import (
	"strings"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/parser"
	"github.com/stretchr/testify/require"
)

func TestChunkIDsAreDeterministic(t *testing.T) {
	doc := models.NewDocument()
	doc.ID = "doc-123"
	doc.Title = "Doc Title"
	doc.Fingerprint = "fp"
	doc.UID = "uid"
	doc.URLs = []string{"https://example.com"}
	doc.Content = "# Doc Title\n\n## Section\nSome content.\n"

	c1 := NewChunker(10_000, 1, 0)
	chunks1, err := c1.ChunkWithHierarchy(doc)
	require.NoError(t, err)
	require.NotEmpty(t, chunks1)

	c2 := NewChunker(10_000, 1, 0)
	chunks2, err := c2.ChunkWithHierarchy(doc)
	require.NoError(t, err)
	require.Len(t, chunks2, len(chunks1))

	for i := range chunks1 {
		require.Equal(t, chunks1[i].ID, chunks2[i].ID)
		require.Equal(t, chunks1[i].DocumentID, chunks2[i].DocumentID)
		require.Equal(t, chunks1[i].HeaderPath, chunks2[i].HeaderPath)
		require.Equal(t, chunks1[i].StartLine, chunks2[i].StartLine)
		require.Equal(t, chunks1[i].EndLine, chunks2[i].EndLine)
	}
}

// TestChunker_KeepsSectionsBelowMinSize pins that no section of a
// document is ever dropped from the index.
//
// min_chunk_size is a MERGE threshold, not a DROP threshold: a
// section shorter than it is folded into its neighbor, never
// discarded. Before the fix, splitByHeaders only appended a section
// when it was already >= minSize, so a document whose sections were
// all short produced zero chunks — the ingest reported success and
// the document was invisible to search.
func TestChunker_KeepsSectionsBelowMinSize(t *testing.T) {
	doc := models.NewDocument()
	doc.ID = "doc-short"
	doc.Title = "Short Doc"
	doc.Fingerprint = "fp"
	doc.UID = "uid-short"
	doc.Content = "# Title\n\nShort intro.\n\n## Tiny\n\nOnly eleven chars.\n"

	c := NewChunker(2000, 300, 150)
	chunks, err := c.ChunkDocument(doc)
	require.NoError(t, err)
	require.NotEmpty(t, chunks, "a document whose sections are all below min_chunk_size must still be indexed")

	joined := strings.Join(chunkContents(chunks), "\n")
	require.Contains(t, joined, "Only eleven chars.",
		"a section below min_chunk_size must be folded into a neighbor, not dropped")
}

// TestChunker_MergesShortSectionIntoPreviousChunk pins that a
// short section lands in the chunk that precedes it, so the text
// stays searchable and the chunk count stays close to the number of
// real sections.
func TestChunker_MergesShortSectionIntoPreviousChunk(t *testing.T) {
	long := strings.Repeat("Long enough content for this section. ", 8)
	doc := models.NewDocument()
	doc.ID = "doc-mixed"
	doc.Title = "Mixed Doc"
	doc.Fingerprint = "fp"
	doc.UID = "uid-mixed"
	doc.Content = strings.Join([]string{
		"# Long Section", "", long, "",
		"## Tiny", "", "Only eleven chars.", "",
		"## Another Long Section", "", long,
	}, "\n")

	c := NewChunker(2000, 300, 150)
	chunks, err := c.ChunkDocument(doc)
	require.NoError(t, err)
	require.Len(t, chunks, 2, "the short section folds into its neighbor instead of adding a chunk")

	joined := strings.Join(chunkContents(chunks), "\n")
	require.Contains(t, joined, "Only eleven chars.",
		"the short section's text must survive the merge")
}

// TestChunker_AcceptsCRLFLineEndings pins the chunker side of
// the fix for issue #82. The parser normalizes CRLF to LF before
// chunking, so the chunks we emit never carry a trailing `\r` on
// any line. The test drives the full ingest path (parser +
// chunker) and asserts the chunk content is clean LF end-to-end.
func TestChunker_AcceptsCRLFLineEndings(t *testing.T) {
	doc := models.NewDocument()
	doc.UID = "uid-crlf"
	doc.Fingerprint = "fp-crlf"
	doc.RawContent = []byte("---\r\nuid: uid-crlf\r\nfingerprint: fp-crlf\r\n---\r\n# Title\r\n\r\nFirst paragraph.\r\n\r\n## Section\r\nbody.\r\n")

	p := parser.NewDocbuilderParser()
	parsed, err := p.ParseDocument(doc.RawContent, "test.md")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	c := NewChunker(10_000, 1, 0)
	chunks, err := c.ChunkWithHierarchy(parsed)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	for _, chunk := range chunks {
		require.NotContains(t, chunk.Content, "\r",
			"chunks must be LF-only after CRLF normalization (chunk ID %q)", chunk.ID)
	}
}

// chunkContents returns the content of every chunk.
func chunkContents(chunks []*models.Chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, chunk.Content)
	}
	return out
}
