package vector

import (
	"context"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestSampleEmbeddingLength_EmptyStoreReturnsNotFound pins the
// contract for an empty collection: SampleEmbeddingLength
// returns (0, false, nil). doctor uses the false return to skip
// the stale-data check entirely ("store is empty, nothing to
// verify") instead of warning about a zero-length mismatch.
func TestSampleEmbeddingLength_EmptyStoreReturnsNotFound(t *testing.T) {
	db, err := NewVectorDB("test", 768, "", "test-model")
	require.NoError(t, err)

	length, found, err := db.SampleEmbeddingLength(context.Background())
	require.NoError(t, err)
	require.False(t, found, "empty store must report found=false")
	require.Equal(t, 0, length)
}

// TestSampleEmbeddingLength_NonEmptyStoreReturnsActualDim pins
// the happy path: after AddChunk with a known dim, the sampler
// reports that dim back. doctor uses this to compare against the
// configured dim and warn on mismatch.
func TestSampleEmbeddingLength_NonEmptyStoreReturnsActualDim(t *testing.T) {
	db, err := NewVectorDB("test", 768, "", "test-model")
	require.NoError(t, err)

	chunk := &models.Chunk{
		ID: "c1", DocumentID: "d1", DocumentTitle: "T", Content: "x",
	}
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = float32(i) * 0.001
	}
	require.NoError(t, db.AddChunk(context.Background(), chunk, vec))

	length, found, err := db.SampleEmbeddingLength(context.Background())
	require.NoError(t, err)
	require.True(t, found, "store with chunks must report found=true")
	require.Equal(t, 768, length, "sampler must report the actual embedding length, not the configured one")
}
