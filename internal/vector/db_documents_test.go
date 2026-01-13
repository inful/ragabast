package vector

import (
	"context"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestVectorDB_GetUniqueDocuments_PopulatesMetadata(t *testing.T) {
	db, err := NewVectorDB("test", 3, "")
	require.NoError(t, err)

	createdAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	updatedAt := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)

	chunks := []*models.Chunk{
		{
			ID:                 "chunk-1",
			DocumentID:         "doc-1",
			DocumentTitle:      "Doc Title",
			Content:            "hello",
			HeaderPath:         "",
			Level:              1,
			StartLine:          1,
			EndLine:            1,
			UID:                "u-1",
			Fingerprint:        "fp-1",
			DocumentURLs:       []string{"https://example.com/a"},
			DocumentTags:       []string{"t1", "t2"},
			DocumentCategories: []string{"c1"},
			DocumentCreatedAt:  createdAt,
			DocumentUpdatedAt:  updatedAt,
		},
		{
			ID:            "chunk-2",
			DocumentID:    "doc-1",
			DocumentTitle: "Doc Title",
			Content:       "world",
			HeaderPath:    "",
			Level:         1,
			StartLine:     2,
			EndLine:       2,
		},
	}

	embeddings := [][]float32{{1, 0, 0}, {0, 1, 0}}
	require.NoError(t, db.AddChunksBatch(context.Background(), chunks, embeddings))

	docs, err := db.GetUniqueDocuments(context.Background())
	require.NoError(t, err)
	require.Len(t, docs, 1)

	require.Equal(t, "doc-1", docs[0].ID)
	require.Equal(t, "u-1", docs[0].UID)
	require.Equal(t, "fp-1", docs[0].Fingerprint)
	require.Equal(t, "Doc Title", docs[0].Title)
	require.Equal(t, []string{"t1", "t2"}, docs[0].Tags)
	require.Equal(t, []string{"c1"}, docs[0].Categories)
	require.Equal(t, []string{"https://example.com/a"}, docs[0].URLs)
	require.Equal(t, createdAt, docs[0].CreatedAt)
	require.Equal(t, updatedAt, docs[0].UpdatedAt)
	require.Equal(t, 2, docs[0].ChunkCount)
}
