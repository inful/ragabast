package service

import (
	"errors"
	"fmt"
	"strings"
)

// ErrVectorStoreCorrupt is returned by the service layer when
// the on-disk vector store has been left in an inconsistent
// state — most commonly, embeddings of two different lengths
// from a model/dimension config change that was not paired with
// a wipe. Callers can use errors.Is to detect this specific
// failure mode and surface a recovery hint.
//
// The error wraps the underlying chromem-go message so the
// original error chain is preserved for debugging, but the
// wrapper string also names the recovery command so a user
// reading the CLI output knows what to do next.
var ErrVectorStoreCorrupt = errors.New("vector store is corrupt")

// chromemCorruptionSignature is the substring of chromem-go's
// "vectors must have the same length" error that indicates the
// collection has mixed-dimension vectors. chromem-go does not
// expose typed errors, so a substring check is the most
// reliable way to detect this case without coupling to chromem-go
// internals.
//
// The substring is stable across chromem-go versions because it
// comes from the cosine-similarity inner loop, not the public
// API surface.
const chromemCorruptionSignature = "vectors must have the same length"

// wrapCorruptionError inspects an error returned by the vector
// layer and, if it matches the chromem-go "mixed dimensions"
// signature, returns an error that wraps ErrVectorStoreCorrupt
// plus actionable recovery guidance. Unrelated errors pass
// through unchanged; nil passes through as nil.
//
// The signature is checked against the unwrapped error chain
// (strings.Contains on err.Error()) so the helper still fires
// when chromem-go's error has been wrapped by upstream layers
// like db.GetUniqueDocuments ("failed to query documents: ...")
// or operations.GetUniqueDocuments.
func wrapCorruptionError(err error) error {
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), chromemCorruptionSignature) {
		return err
	}
	return fmt.Errorf(
		"%w: %v. "+
			"This usually means the embedding model or ollama.embedding_dimensions "+
			"changed since these vectors were stored, leaving the collection with "+
			"inconsistent dimensions. Recover by stopping the server, running "+
			"`ragabast vector reset --force`, then `ragabast ingest` to rebuild "+
			"the collection from the source documents.",
		ErrVectorStoreCorrupt, err,
	)
}
