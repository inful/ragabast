package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWrapCorruptionError_DimensionMismatch pins the contract
// that a chromem-go "vectors must have the same length" error
// comes out of the service layer with actionable recovery
// guidance rather than the raw four-level "couldn't" chain.
// This is the single most common failure mode after an
// embedding-model or dimension config change.
func TestWrapCorruptionError_DimensionMismatch(t *testing.T) {
	raw := errors.New("chromem: couldn't calculate similarity for document 'abc': vectors must have the same length")

	wrapped := wrapCorruptionError(raw)

	require.Error(t, wrapped)
	require.Contains(t, wrapped.Error(), "ragabast vector reset --force",
		"the wrapped error must point the user at the recovery command")
	require.Contains(t, wrapped.Error(), "embedding model",
		"the wrapped error must explain the most likely cause")
	require.Contains(t, wrapped.Error(), "vectors must have the same length",
		"the underlying chromem-go message must be preserved for debugging")
	require.ErrorIs(t, wrapped, ErrVectorStoreCorrupt,
		"callers can use errors.Is(err, service.ErrVectorStoreCorrupt) to detect this case")
}

// TestWrapCorruptionError_UnrelatedErrorPassesThrough pins that
// errors without the chromem-go length-mismatch signature are
// returned unchanged (no false positives).
func TestWrapCorruptionError_UnrelatedErrorPassesThrough(t *testing.T) {
	raw := errors.New("network: connection refused")

	wrapped := wrapCorruptionError(raw)

	require.ErrorIs(t, wrapped, raw,
		"unrelated errors must pass through unchanged so callers don't see a false 'corrupt' diagnosis")
}

// TestWrapCorruptionError_NilPassesThrough pins that nil in ->
// nil out (so the helper is safe to call unconditionally).
func TestWrapCorruptionError_NilPassesThrough(t *testing.T) {
	require.NoError(t, wrapCorruptionError(nil))
}

// TestWrapCorruptionError_MatchesAcrossWrapLayers pins that
// the signature still fires when the chromem-go error has been
// wrapped by upstream layers (db.GetUniqueDocuments wraps with
// "failed to query documents: ..."). This is the shape the user
// actually sees today.
func TestWrapCorruptionError_MatchesAcrossWrapLayers(t *testing.T) {
	raw := errors.New("failed to query documents: couldn't get most similar docs: couldn't calculate similarity for document 'abc': vectors must have the same length")
	wrapped := wrapCorruptionError(raw)
	require.Contains(t, wrapped.Error(), "ragabast vector reset --force",
		"the helper must still detect the signature through wrapping layers")
}
