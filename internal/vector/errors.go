package vector

import "errors"

// ErrNotFound is the sentinel returned by VectorDB methods
// (and propagated up through VectorOperations / Service)
// when the requested document, chunk, or document-id has
// no rows in the underlying store.
//
// Callers should match it with errors.Is rather than ==
// so wrapped errors (e.g. fmt.Errorf("...: %w", ErrNotFound))
// still match. The HTTP layer maps this to 404.
//
// The sentinel is package-level rather than per-method so
// callers don't need to import a different error variable
// for each operation; the underlying vector code paths are
// uniform in what "not found" means at the document level.
var ErrNotFound = errors.New("document not found")
