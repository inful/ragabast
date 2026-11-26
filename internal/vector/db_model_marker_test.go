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

// TestVectorDB_UnknownModelSkipsMarkerCheck pins the "unknown
// model" case: an operator who never declared a model name (the
// /v2/embed path has no model field on the wire) must not be
// blocked by a marker written under another provider's name.
// The check is skipped, not failed.
func TestVectorDB_UnknownModelSkipsMarkerCheck(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, modelMarkerFile),
		[]byte("nomic-embed-text:v1.5"), 0o600))

	db, err := NewVectorDB("test", 4, dir, "")
	require.NoError(t, err, "an undeclared model must not trip the marker check")
	require.NotNil(t, db)
}

// TestVectorDB_RejectsMismatchedGemmaModel pins the model swap
// detection on the /v2/embed path: the operator declares which
// model the server runs (embedding_gemma.model), and swapping it
// is caught exactly like the OpenAI-compat path.
func TestVectorDB_RejectsMismatchedGemmaModel(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, modelMarkerFile),
		[]byte("embeddinggemma"), 0o600))

	_, err := NewVectorDB("test", 768, dir, "embeddinggemma-2")
	require.Error(t, err, "a model swap must be rejected before the server serves searches")
	assert.Contains(t, err.Error(), "embeddinggemma",
		"error must name the persisted model")
	assert.Contains(t, err.Error(), "embeddinggemma-2",
		"error must name the configured model")
	assert.Contains(t, err.Error(), "ragabast vector reset --force",
		"error must name the recovery command")
}

// TestModelMismatchError_CarriesBothModels pins the typed error:
// callers that want to act on a mismatch (quarantine the stale
// store) need both model names without parsing the message, and
// errors.Is must keep matching the ErrModelMismatch sentinel.
func TestModelMismatchError_CarriesBothModels(t *testing.T) {
	mismatch := &ModelMismatchError{Persisted: "embeddinggemma", Configured: "embeddinggemma-2"}

	assert.Equal(t, "embeddinggemma", mismatch.Persisted)
	assert.Equal(t, "embeddinggemma-2", mismatch.Configured)
	require.ErrorIs(t, mismatch, ErrModelMismatch,
		"the typed error must still satisfy errors.Is(err, ErrModelMismatch)")
	assert.Contains(t, mismatch.Error(), "embeddinggemma")
}

// TestQuarantineStore_MovesStoreAsideAndRecreatesDir pins the
// invalidation behavior: the stale store is MOVED, not deleted
// (a mistyped model name must be recoverable), and the original
// path exists again so a fresh store can open there.
func TestQuarantineStore_MovesStoreAsideAndRecreatesDir(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "vectors")
	require.NoError(t, os.MkdirAll(filepath.Join(storeDir, "search"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, modelMarkerFile),
		[]byte("old-model"), 0o600))

	stale, err := QuarantineStore(storeDir, "")
	require.NoError(t, err)
	require.NotEmpty(t, stale, "the quarantine path must be reported so the operator can find it")
	assert.DirExists(t, stale, "the stale store must still be on disk")
	assert.FileExists(t, filepath.Join(stale, modelMarkerFile),
		"the stale store keeps its marker — nothing is lost")
	assert.DirExists(t, storeDir, "the original path must exist again for the fresh store")
}

// TestQuarantineStore_MovesSeparateKeywordIndex pins the split-store
// setup: a keyword index outside the persistence dir must move too,
// otherwise it keeps chunks whose vectors are gone.
func TestQuarantineStore_MovesSeparateKeywordIndex(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "vectors")
	kwDir := filepath.Join(dir, "keyword")
	require.NoError(t, os.MkdirAll(storeDir, 0o755))
	require.NoError(t, os.MkdirAll(kwDir, 0o755))

	stale, err := QuarantineStore(storeDir, kwDir)
	require.NoError(t, err)
	require.NotEmpty(t, stale)
	assert.NoDirExists(t, kwDir, "the keyword index must be moved aside too")
	matches, globErr := filepath.Glob(kwDir + ".stale-*")
	require.NoError(t, globErr)
	assert.Len(t, matches, 1, "the keyword index must be recoverable next to its old path")
}

// TestQuarantineStore_MissingDirIsNotAnError pins the empty-store
// case: nothing to invalidate is not a failure.
func TestQuarantineStore_MissingDirIsNotAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	stale, err := QuarantineStore(dir, "")
	require.NoError(t, err)
	assert.Empty(t, stale, "nothing was moved, so nothing is reported")
}
