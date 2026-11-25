package models

import "errors"

// Validation errors.
var (
	ErrMissingFingerprint = errors.New("document fingerprint is required")
	ErrMissingUID         = errors.New("document UID is required")
	ErrMissingURL         = errors.New("at least one URL is required")
	ErrMissingContent     = errors.New("document content is required")
	ErrInvalidFrontmatter = errors.New("invalid frontmatter format")
	ErrParseFailed        = errors.New("failed to parse document")

	// ErrInvalidDocument marks a document the server refused
	// because of its content: unparsable frontmatter, invalid
	// YAML, a missing required field. Callers use errors.Is to
	// tell a bad document (the caller's fault) from a
	// server-side failure (the server's fault) and map them to
	// different HTTP statuses.
	ErrInvalidDocument = errors.New("invalid document")
)

// invalidDocumentError marks err as a document-level failure
// without changing its message: the message is what operators and
// clients read, and existing log lines match on it.
type invalidDocumentError struct{ err error }

func (e *invalidDocumentError) Error() string { return e.err.Error() }

func (e *invalidDocumentError) Unwrap() error { return e.err }

func (e *invalidDocumentError) Is(target error) bool { return target == ErrInvalidDocument }

// InvalidDocument marks err as a document-level failure: the
// document itself is bad, not the server. The message is preserved
// verbatim; errors.Is(err, ErrInvalidDocument) is how callers
// detect it.
func InvalidDocument(err error) error {
	if err == nil {
		return nil
	}
	return &invalidDocumentError{err: err}
}

// Database errors.
var (
	ErrDocumentNotFound = errors.New("document not found")
	ErrChunkNotFound    = errors.New("chunk not found")
	ErrSearchFailed     = errors.New("search operation failed")
	ErrNotFound         = errors.New("not found")
	// ErrEmbeddingDimensionMismatch is returned by the vector DB
	// when an incoming chunk's embedding length differs from the
	// collection's configured dimension. The wrapped error includes
	// the actual and configured lengths so the user can diagnose
	// the mismatch (usually: changed embedding model or
	// ollama.embedding_dimensions without wiping data/vectors/).
	ErrEmbeddingDimensionMismatch = errors.New("embedding dimension mismatch")
)

// LLM errors.
var (
	ErrEmbeddingFailed  = errors.New("embedding generation failed")
	ErrGenerationFailed = errors.New("text generation failed")
	ErrOllamaNotRunning = errors.New("ollama service not running")
	ErrModelNotFound    = errors.New("model not found")
)

// Processing errors.
var (
	ErrInvalidChunkSize = errors.New("invalid chunk size")
	ErrEmptyDocument    = errors.New("document is empty")
	ErrInvalidFormat    = errors.New("invalid document format")
	ErrInvalidInput     = errors.New("invalid input")
)
