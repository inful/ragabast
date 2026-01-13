package vector

import (
	"context"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestVectorDB_DocumentNeedsUpdate(t *testing.T) {
	db, err := NewVectorDB("test", 3, "")
	require.NoError(t, err)

	needsUpdate, exists, err := db.DocumentNeedsUpdate(context.Background(), "doc-1", "fp-1")
	require.NoError(t, err)
	require.True(t, needsUpdate)
	require.False(t, exists)

	chunk := &models.Chunk{ID: "c1", DocumentID: "doc-1", Content: "hello", DocumentTitle: "t", Fingerprint: "fp-1"}
	require.NoError(t, db.AddChunk(context.Background(), chunk, []float32{1, 0, 0}))

	needsUpdate, exists, err = db.DocumentNeedsUpdate(context.Background(), "doc-1", "fp-1")
	require.NoError(t, err)
	require.False(t, needsUpdate)
	require.True(t, exists)

	needsUpdate, exists, err = db.DocumentNeedsUpdate(context.Background(), "doc-1", "fp-2")
	require.NoError(t, err)
	require.True(t, needsUpdate)
	require.True(t, exists)
}
