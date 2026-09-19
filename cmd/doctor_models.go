package cmd

// knownModelDimensions maps well-known embedding model names
// to the dimension they emit when called without a Matryoshka /
// dimensions request. The map is used by `ragabast doctor` to
// pre-flight a config and warn before the user hits an
// "embedding dimension mismatch" at ingest time.
//
// The map is intentionally small and curated. Unknown models
// are reported as such rather than guessed at; the doctor
// output must never silently pass an unknown model. Add an
// entry when a model you care about starts showing up as
// "unknown" in `ragabast doctor` output.
//
// Notes:
//   - Matryoshka-capable models (jina v5, OpenAI text-embedding-3-*)
//     have multiple sizes; the value listed here is the FULL
//     size the server returns when no `dimensions` request is
//     made. With `ollama.embedding_dimensions: N` set, the
//     actual returned size will be N — the doctor notes this.
//   - Google's `gemini-embedding-2` is multimodal and returns
//     3072 dims by default; `outputDimensionality` (native API
//     only) can lower it. The OpenAI-compat layer always
//     returns 3072.
//   - Some Ollama model names appear with and without a tag
//     (e.g. "nomic-embed-text" vs "nomic-embed-text:v1.5").
//     Map both.
var knownModelDimensions = map[string]int{
	// Ollama / local sentence-transformers
	"nomic-embed-text":               768,
	"nomic-embed-text:v1.5":          768,
	"nomic-ai/nomic-embed-text-v1.5": 768,
	"mxbai-embed-large":              1024,
	"mxbai-embed-large:335m":         1024,
	"snowflake-arctic-embed":         1024,
	"snowflake-arctic-embed:275m":    1024,
	"all-minilm":                     384,
	"all-minilm:l6-v2":               384,

	// OpenAI
	"text-embedding-3-small": 1536,
	"text-embedding-3-large": 3072,
	"text-embedding-ada-002": 1536,

	// jina (Matryoshka-capable: 32, 64, 128, 256, 512, 768, 1024)
	"jina-embeddings-v5-text-small":        1024,
	"jinaai/jina-embeddings-v5-text-small": 1024,
	"jina-embeddings-v3":                   1024,
	"jinaai/jina-embeddings-v3":            1024,

	// Google (OpenAI-compat layer at
	// generativelanguage.googleapis.com/v1beta/openai)
	"gemini-embedding-001": 768,
	"gemini-embedding-2":   3072,
}
