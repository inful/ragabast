package chunker

import (
	"testing"

	"github.com/ragabast/internal/models"
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
