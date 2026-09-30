package chunker

import (
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
