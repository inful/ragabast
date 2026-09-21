package vector

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVectorDB_WritesModelMarkerOnFirstAdd pins the
// persistence contract: the first AddChunk writes the
// embedding model name to a marker file at the persistence
// directory root. Operators rely on this marker to detect
// mismatched-model state after a config swap.
func TestVectorDB_WritesModelMarkerOnFirstAdd(t *testing.T) {
	dir := t.TempDir()
	db, err := NewVectorDB("test", 4, dir, "nomic-embed-text:v1.5")
	require.NoError(t, err)

	// Before any data is added, the marker file does not
	// exist — the dir is fresh and no model has been
	// committed yet.
	if _, statErr := os.Stat(filepath.Join(dir, modelMarkerFile)); !os.IsNotExist(statErr) {
		t.Fatalf("fresh dir must not yet have a marker file; stat err: %v", statErr)
	}

	// Add one chunk to trigger the marker write.
	chunk := &models.Chunk{ID: "c1", DocumentID: "doc-1"}
	vec := make([]float32, 4)
	require.NoError(t, db.AddChunk(context.Background(), chunk, vec))

	data, err := os.ReadFile(filepath.Join(dir, modelMarkerFile))
	require.NoError(t, err, "marker file must exist after first AddChunk")
	assert.Equal(t, "nomic-embed-text:v1.5", string(data),
		"marker must contain the configured embedding model")
}

// TestVectorDB_NoMarkerForFreshOpen pins that opening a fresh
// dir does NOT write a marker. The marker is written only when
// data is actually committed. Operators can detect "fresh
// state" by the absence of the marker (or by absence of chunks,
// both equivalent).
func TestVectorDB_NoMarkerForFreshOpen(t *testing.T) {
	dir := t.TempDir()
	_, err := NewVectorDB("test", 4, dir, "any-model")
	require.NoError(t, err)

	if _, statErr := os.Stat(filepath.Join(dir, modelMarkerFile)); !os.IsNotExist(statErr) {
		t.Fatalf("NewVectorDB alone must not write the marker; stat err: %v", statErr)
	}
}

// TestVectorDB_RejectsMismatchedModel is the headline acceptance
// criterion from issue #19: opening a persistence dir whose
// marker carries a different model name returns an error.
// Operators get a hard stop before the server serves searches
// against the wrong model.
func TestVectorDB_RejectsMismatchedModel(t *testing.T) {
	dir := t.TempDir()

	// Seed a marker for a different model.
	require.NoError(t, os.WriteFile(filepath.Join(dir, modelMarkerFile),
		[]byte("jina-v5-base"), 0o600))

	_, err := NewVectorDB("test", 4, dir, "nomic-embed-text:v1.5")
	require.Error(t, err, "mismatched model must be rejected")
	assert.Contains(t, err.Error(), "jina-v5-base",
		"error must mention the persisted model so the operator can see what they have")
	assert.Contains(t, err.Error(), "nomic-embed-text",
		"error must mention the configured model so the operator can see what they expected")
}

// TestVectorDB_AllowsMatchingModel pins the happy path: opening
// a persistence dir whose marker carries the same model name
// as the configured model succeeds. (The dimensions match by
// construction here — the existing dim check covers that.)
func TestVectorDB_AllowsMatchingModel(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, modelMarkerFile),
		[]byte("nomic-embed-text:v1.5"), 0o600))

	db, err := NewVectorDB("test", 4, dir, "nomic-embed-text:v1.5")
	require.NoError(t, err)
	require.NotNil(t, db)
}

// TestVectorDB_AllowMismatchWhenOverrideSet covers the
// operator override: a config flag can downgrade the failure
// mode from "reject" to "warn and proceed". This is for the
// case where the operator has migrated the data manually (or
// accepted the risk during a phased rollout).
func TestVectorDB_AllowMismatchWhenOverrideSet(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, modelMarkerFile),
		[]byte("jina-v5-base"), 0o600))

	db, err := NewVectorDBWithOptions(Options{
		Name:                "test",
		EmbeddingDimension:  4,
		PersistenceDir:      dir,
		EmbeddingModel:      "nomic-embed-text:v1.5",
		FailOnModelMismatch: false,
	})
	require.NoError(t, err, "override must downgrade the failure mode")
	require.NotNil(t, db)
}
