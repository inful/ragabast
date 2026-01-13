package vector

import (
	"context"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestVectorDB_Search_ReturnsDocumentURLs(t *testing.T) {
	t.Parallel()

	db, err := NewVectorDB("test", 3, "")
	require.NoError(t, err)

	chunk := &models.Chunk{
		ID:            "chunk-1",
		DocumentID:    "doc-1",
		DocumentTitle: "Doc Title",
		Content:       "hello world",
		HeaderPath:    "",
		Level:         1,
		StartLine:     1,
		EndLine:       1,
		DocumentURLs:  []string{"https://example.com/a", "https://example.com/b"},
	}

	emb := []float32{1, 0, 0}
	require.NoError(t, db.AddChunk(context.Background(), chunk, emb))

	results, err := db.Search(context.Background(), emb, 1, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, []string{"https://example.com/a", "https://example.com/b"}, results[0].DocumentURLs)
}
