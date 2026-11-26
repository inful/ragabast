package config

import "time"

// EmbeddingGemmaConfig holds the embedding-gemma /v2/embed
// server settings. Distinct from OllamaConfig because the
// wire shape is non-standard:
//
//   - Path is /v2/embed, not /v1/embeddings.
//   - Body is {"texts": [...]}, not {"input": [...], "model": "..."}.
//   - No Authorization header.
//   - No `dimensions` request field — the server picks the
//     dimension; configure vectordb.embedding_dimension to
//     match what the server returns.
//
// Only consulted when Config.EmbeddingProvider is
// "embedding_gemma". Empty / missing BaseURL is a config
// error in that case (see Config.Validate).
type EmbeddingGemmaConfig struct {
	// BaseURL is the server root. The client appends
	// "/v2/embed" itself; trailing /v1 segments are
	// tolerated and stripped (matches the OpenAI
	// client's normalization so operators can paste
	// either form).
	BaseURL string `env:"EMBEDDING_GEMMA_BASE_URL" yaml:"base_url"`

	// Model names the model the server runs, e.g.
	// "embeddinggemma" or "embeddinggemma-2".
	//
	// The /v2/embed wire shape has no `model` field — the
	// server picks the model itself — so this value is NOT
	// sent on the wire. It exists so ragabast can record
	// which model wrote the vector store: the store's
	// model marker then catches a model swap at startup
	// (the server refuses to open a store written by a
	// different model and names `ragabast vector reset
	// --force` as the fix). Without it, switching models
	// is invisible to ragabast, and re-ingesting the same
	// documents is a no-op because the (uid, fingerprint)
	// dedupe skips them — leaving the old model's vectors
	// in place.
	//
	// Optional: empty means "unknown", and the marker check
	// is skipped.
	Model string `env:"EMBEDDING_GEMMA_MODEL" yaml:"model,omitempty"`

	// Timeout is the per-request HTTP timeout. Default
	// 30s when <= 0.
	Timeout time.Duration `env:"EMBEDDING_GEMMA_TIMEOUT" yaml:"timeout"`

	// EmbeddingDimensions is the configured dimension
	// the operator expects the server to return. 0
	// disables the mismatch check. The /v2/embed endpoint
	// does NOT accept a `dimensions` request field —
	// operators have to match the server-side setting
	// via vectordb.embedding_dimension. This field is
	// only used to detect mismatches and log a one-shot
	// warning if the server returns a different size.
	EmbeddingDimensions int `env:"EMBEDDING_GEMMA_DIMENSIONS" yaml:"embedding_dimensions"`

	// EmbeddingConcurrency controls the parallel
	// worker pool that GenerateChunkEmbeddings uses on
	// the ingest path. Same semantics as
	// OllamaConfig.EmbeddingConcurrency — chunks are
	// split into min(concurrency, len(chunks)) equal
	// sub-batches and processed concurrently. Default 1
	// (sequential). Set higher to match the upstream
	// server's request parallelism.
	EmbeddingConcurrency int `env:"EMBEDDING_GEMMA_CONCURRENCY" yaml:"embedding_concurrency"`

	// EmbeddingDocPrompt is prepended to every chunk
	// text before POSTing to /v2/embed on the doc side.
	// Default empty — preserves the historical raw-text
	// behavior for non-prompted models. Operators
	// using Google's embedding-gemma should set this to
	// "title: none | text: " so the model produces
	// retrieval-optimized document vectors.
	EmbeddingDocPrompt string `env:"EMBEDDING_GEMMA_DOC_PROMPT" yaml:"embedding_doc_prompt,omitempty"`

	// EmbeddingQueryPrompt is prepended to the user
	// query before POSTing to /v2/embed on the query
	// side (EmbedQuery). Same default-empty behavior
	// as EmbeddingDocPrompt. For embedding-gemma the
	// recommended value is "task: search result |
	// query: ". Independent from EmbeddingDocPrompt so
	// operators can tune the two sides separately.
	EmbeddingQueryPrompt string `env:"EMBEDDING_GEMMA_QUERY_PROMPT" yaml:"embedding_query_prompt,omitempty"`
}
