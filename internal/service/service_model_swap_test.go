package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modelSwapConfig returns a config whose vector store lives in a
// temp dir and whose embedder is the declared /v2/embed model.
func modelSwapConfig(t *testing.T, model string) *config.Config {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.VectorDB.PersistenceDir = filepath.Join(t.TempDir(), "vectors")
	cfg.VectorDB.EmbeddingDimension = 768
	cfg.EmbeddingProvider = "embedding_gemma"
	cfg.EmbeddingGemma.BaseURL = "http://127.0.0.1:1"
	cfg.EmbeddingGemma.Model = model

	return cfg
}

// seedModelMarker writes a model marker into the store dir, the way
// a previous ingest would have.
func seedModelMarker(t *testing.T, dir, model string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".embedding_model"),
		[]byte(model), 0o600))
}

// TestNewService_EmbedderChange_InvalidatesStaleStore pins the
// headline behavior: when the store on disk was embedded with a
// different model, the stale vectors are invalidated (moved aside,
// not deleted) and the service starts with an empty store. The
// stale vectors live in the old model's vector space, so keeping
// them would serve nonsense.
func TestNewService_EmbedderChange_InvalidatesStaleStore(t *testing.T) {
	cfg := modelSwapConfig(t, "embeddinggemma-2")
	seedModelMarker(t, cfg.VectorDB.PersistenceDir, "embeddinggemma")

	svc, err := NewService(cfg)
	require.NoError(t, err, "an embedder change must not block startup")
	require.NotNil(t, svc)

	// The stale store moved aside and is still recoverable.
	matches, err := filepath.Glob(cfg.VectorDB.PersistenceDir + ".stale-*")
	require.NoError(t, err)
	require.Len(t, matches, 1, "the stale store must be moved aside, not deleted")
	assert.FileExists(t, filepath.Join(matches[0], ".embedding_model"),
		"the stale copy keeps its marker — nothing is lost")

	// The original path is back, ready for a fresh store.
	assert.DirExists(t, cfg.VectorDB.PersistenceDir)
}

// TestNewService_EmbedderChange_FailOnMismatchStopsStartup pins the
// opt-out: an operator who would rather look before anything moves
// gets the hard stop with the recovery command.
func TestNewService_EmbedderChange_FailOnMismatchStopsStartup(t *testing.T) {
	cfg := modelSwapConfig(t, "embeddinggemma-2")
	cfg.VectorDB.FailOnModelMismatch = true
	seedModelMarker(t, cfg.VectorDB.PersistenceDir, "embeddinggemma")

	_, err := NewService(cfg)
	require.Error(t, err, "fail_on_model_mismatch must stop the startup")
	assert.Contains(t, err.Error(), "embeddinggemma")
	assert.Contains(t, err.Error(), "ragabast vector reset --force",
		"the error must name the recovery command")

	matches, globErr := filepath.Glob(cfg.VectorDB.PersistenceDir + ".stale-*")
	require.NoError(t, globErr)
	assert.Empty(t, matches, "nothing may move when the operator asked for a hard stop")
}

// TestNewService_SameEmbedder_KeepsStore pins the no-op case: the
// same model on both sides must leave the corpus alone.
func TestNewService_SameEmbedder_KeepsStore(t *testing.T) {
	cfg := modelSwapConfig(t, "embeddinggemma-2")
	seedModelMarker(t, cfg.VectorDB.PersistenceDir, "embeddinggemma-2")

	_, err := NewService(cfg)
	require.NoError(t, err)

	matches, globErr := filepath.Glob(cfg.VectorDB.PersistenceDir + ".stale-*")
	require.NoError(t, globErr)
	assert.Empty(t, matches, "an unchanged embedder must not invalidate anything")
	assert.FileExists(t, filepath.Join(cfg.VectorDB.PersistenceDir, ".embedding_model"),
		"the marker must survive")
}
