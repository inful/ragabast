package vector

import (
	"context"
	"strings"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestAddChunksBatch_RejectsMismatchedDimension pins the contract
// that an ingest cannot store a vector whose length differs from
// the collection's configured embedding_dimension. Without this
// check, a chromem-go similarity calculation later panics with
// "vectors must have the same length" the moment a query hits a
// chunk with a different shape than the rest of the collection.
//
// The realistic flow that produced this bug:
//  1. User ingests with embedding_dimensions: 0 (full size, 768).
//  2. User flips embedding_dimensions: 256 (Matryoshka) and
//     re-ingests without wiping data/vectors/.
//  3. New chunks land at 256 dims; old chunks remain at 768.
//  4. Search compares a 768-dim vector against a 256-dim vector.
//     chromem-go returns "vectors must have the same length"
//     and the user sees a 500.
func TestAddChunksBatch_RejectsMismatchedDimension(t *testing.T) {
	// Collection is configured for 768 dims (e.g. nomic-embed-text-v1.5).
	db, err := NewVectorDB("test", 768, "", "test-model")
	require.NoError(t, err)

	// Pretend the embeddings server returned a 256-dim vector
	// because the user set embedding_dimensions: 256 and the
	// server honored it.
	wrongDim := make([]float32, 256)
	for i := range wrongDim {
		wrongDim[i] = float32(i) * 0.001
	}
	rightDim := make([]float32, 768)
	for i := range rightDim {
		rightDim[i] = float32(i) * 0.001
	}

	chunks := []*models.Chunk{
		{ID: "c1", DocumentID: "d1", DocumentTitle: "T", Content: "good"},
		{ID: "c2", DocumentID: "d1", DocumentTitle: "T", Content: "wrong size"},
	}
	embeddings := [][]float32{rightDim, wrongDim}

	err = db.AddChunksBatch(context.Background(), chunks, embeddings)
	require.Error(t, err, "AddChunksBatch must reject when any embedding length != configured dimension")
	require.Contains(t, err.Error(), "768",
		"error must mention the configured dimension so the user can diagnose")
	require.Contains(t, err.Error(), "256",
		"error must mention the actual offending length")
	require.Contains(t, err.Error(), "vector reset --force",
		"error must point at one of the recovery paths (stale data -> reset + reingest)")
	require.Contains(t, err.Error(), "vectordb.embedding_dimension",
		"error must point at the other recovery path (config mismatch -> update vectordb.embedding_dimension)")
	require.Contains(t, err.Error(), "ragabast doctor",
		"error must point at the doctor command for diagnosis")
}

// TestAddChunksBatch_AcceptsMatchingDimension is a control test:
// the happy path (all vectors match the configured dimension)
// must still succeed.
func TestAddChunksBatch_AcceptsMatchingDimension(t *testing.T) {
	db, err := NewVectorDB("test", 4, "", "test-model")
	require.NoError(t, err)

	vec1 := []float32{1, 2, 3, 4}
	vec2 := []float32{5, 6, 7, 8}
	chunks := []*models.Chunk{
		{ID: "c1", DocumentID: "d1", DocumentTitle: "T", Content: "a"},
		{ID: "c2", DocumentID: "d1", DocumentTitle: "T", Content: "b"},
	}
	err = db.AddChunksBatch(context.Background(), chunks, [][]float32{vec1, vec2})
	require.NoError(t, err)
}

// TestAddChunk_RejectsMismatchedDimension pins the same contract
// for the single-chunk AddChunk path. The single-chunk path is
// what `ragabast ingest` of a single small file hits; both paths
// must guard.
func TestAddChunk_RejectsMismatchedDimension(t *testing.T) {
	db, err := NewVectorDB("test", 16, "", "test-model")
	require.NoError(t, err)

	wrong := make([]float32, 8) // half the expected size
	for i := range wrong {
		wrong[i] = float32(i) * 0.01
	}

	chunk := &models.Chunk{ID: "c1", DocumentID: "d1", DocumentTitle: "T", Content: "x"}
	err = db.AddChunk(context.Background(), chunk, wrong)
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "16") || strings.Contains(err.Error(), "8"),
		"error must mention both configured (16) and actual (8) length, got: %s", err.Error())
}
