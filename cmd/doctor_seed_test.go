package cmd

import (
	"context"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/require"
)

// seedVectorDB is a thin wrapper over *vector.VectorDB used by
// the doctor tests to construct a "store already has data"
// scenario without going through the full service layer.
type seedVectorDB struct {
	db *vector.VectorDB
}

// newSeedVectorDBWithModelAt is the model-aware form used by
// tests that need the seed to match the doctor's configured
// model (so the model-mismatch check doesn't short-circuit the
// dimension-mismatch scenario the test is exercising).
//
// The model name is stamped on the persistence dir on the
// first addChunk call (matching production behavior added
// in issue #19). Tests pass the same model the doctor
// command's config will use so the model's marker matches
// the configured model — otherwise the doctor's
// model-mismatch check fires before the test's
// dimension-mismatch scenario has a chance to.
func newSeedVectorDBWithModelAt(persistDir string, dim int, model string) (*seedVectorDB, error) {
	db, err := vector.NewVectorDB("test", dim, persistDir, model)
	if err != nil {
		return nil, err
	}
	return &seedVectorDB{db: db}, nil
}

// newSeedVectorDBWithModel is the model-aware public form for
// tests that need the seed to match the doctor's configured
// model.
func newSeedVectorDBWithModel(persistDir string, dim int, model string) (*seedVectorDB, error) {
	return newSeedVectorDBWithModelAt(persistDir, dim, model)
}

// addChunk is the seed-store write path. It bypasses the
// dimension guard in AddChunk because the seed DB was built
// with the same dim the chunk will carry — there is no
// mismatch to guard against.
func (s *seedVectorDB) addChunk(chunk *modelsChunkForSeed, embedding []float32) error {
	c := &models.Chunk{
		ID:            chunk.ID,
		DocumentID:    chunk.DocumentID,
		DocumentTitle: chunk.DocumentTitle,
		HeaderPath:    chunk.HeaderPath,
		Level:         chunk.Level,
		StartLine:     chunk.StartLine,
		EndLine:       chunk.EndLine,
		Content:       chunk.Content,
		Fingerprint:   chunk.DocumentFingerprint,
		UID:           chunk.UID,
	}
	return s.db.AddChunk(context.Background(), c, embedding)
}

// modelsChunkForSeed is a minimal Chunk-shaped struct the
// doctor tests construct. It exists as a separate type from
// *models.Chunk so the tests don't need to populate every
// field that models.Chunk exposes (the seed only needs ID and
// DocumentID). The helper translates it to a real models.Chunk.
type modelsChunkForSeed struct {
	ID                  string
	DocumentID          string
	DocumentTitle       string
	HeaderPath          string
	Level               int
	StartLine           int
	EndLine             int
	Content             string
	DocumentFingerprint string
	UID                 string
}

// require.NoError assertion imported for tests that pull in
// this file indirectly. Keeps the import path stable if the
// helpers change later.
var _ = require.NoError
