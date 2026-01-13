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
)

// Database errors.
var (
	ErrDocumentNotFound = errors.New("document not found")
	ErrChunkNotFound    = errors.New("chunk not found")
	ErrSearchFailed     = errors.New("search operation failed")
	ErrNotFound         = errors.New("not found")
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
