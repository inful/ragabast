package vector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ragabast/internal/models"
)

// EmbeddingClient is the embedding-provider surface that
// VectorOperations depends on. Two concrete implementations
// live in this package today:
//
//   - *OpenAIEmbeddingClient — the default; speaks the
//     OpenAI /v1/embeddings protocol used by Ollama, vLLM,
//     LM Studio, llama.cpp --embedding, llama-stack, and
//     OpenAI itself.
//   - *EmbeddingGemmaClient — speaks the EmbeddingGemma
//     /v2/embed protocol. Wire shape: POST {"texts": [...]},
//     no `model` or `dimensions` field, no Authorization
//     header.
//
// The interface exists so service.NewService and the doctor
// check can pick the right client from config without
// VectorOperations caring which one it is. Tests can swap in
// a fake by satisfying these five methods.
type EmbeddingClient interface {
	// GenerateEmbedding returns one vector for the given
	// text. The docPrompt (if configured) is prepended
	// before the request. Use this for the ingest path and
	// for the doctor probe.
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)

	// EmbedQuery returns one vector for a user query. The
	// queryPrompt (if configured) is prepended instead of
	// the docPrompt — this split matters for models whose
	// document and query embeddings are trained
	// asymmetrically (embedding-gemma, jina v5, OpenAI
	// text-embedding-3-*).
	EmbedQuery(ctx context.Context, query string) ([]float32, error)

	// GenerateChunkEmbedding is the chunk-shaped single
	// call. Implementations typically reuse
	// GenerateEmbedding with the chunk's full path.
	GenerateChunkEmbedding(ctx context.Context, chunk *models.Chunk) ([]float32, error)

	// GenerateChunkEmbeddings is the chunk-shaped batch
	// call. The docPrompt (if configured) is prepended to
	// every chunk before the request.
	GenerateChunkEmbeddings(ctx context.Context, chunks []*models.Chunk) ([][]float32, error)

	// ValidateConnection checks that the configured
	// embeddings server is reachable. Returns nil on a
	// successful 2xx from the server's health/listing
	// endpoint. Used at startup and by `ragabast doctor`.
	ValidateConnection(ctx context.Context) error
}

// EmbeddingProvider is the discriminator that selects which
// concrete EmbeddingClient the factory returns. The string
// form is what appears in config: empty / "openai" maps to
// the OpenAI-compat client; "embedding_gemma" maps to the
// EmbeddingGemma /v2/embed client.
type EmbeddingProvider string

const (
	// EmbeddingProviderOpenAI is the default. Speaks the
	// OpenAI /v1/embeddings protocol via ollama.base_url.
	EmbeddingProviderOpenAI EmbeddingProvider = "openai"

	// EmbeddingProviderEmbeddingGemma speaks the
	// EmbeddingGemma /v2/embed protocol via
	// embedding_gemma.base_url. Wire shape is fixed by the
	// server: POST {"texts":[...]} with no model /
	// dimensions / Authorization fields.
	EmbeddingProviderEmbeddingGemma EmbeddingProvider = "embedding_gemma"
)

// EmbeddingClientOptions is the package-neutral subset of
// configuration every concrete EmbeddingClient needs.
// service.NewService and cmd/root.go both assemble one of
// these from the relevant config block, then hand it to
// NewEmbeddingClientFromOptions — this keeps the factory
// free of any package-level config dependency and makes it
// trivial to test in isolation.
type EmbeddingClientOptions struct {
	// Provider selects the concrete client. Empty is
	// treated as EmbeddingProviderOpenAI.
	Provider EmbeddingProvider

	// OpenAI fields (used when Provider is openai).
	OpenAIBaseURL     string
	OpenAIModel       string
	OpenAITimeout     time.Duration
	OpenAIAPIKey      string
	OpenAIDimensions  int
	OpenAIConcurrency int
	OpenAIDocPrompt   string
	OpenAIQueryPrompt string

	// EmbeddingGemma fields (used when Provider is
	// embedding_gemma). BaseURL is required when Provider
	// is embedding_gemma.
	EmbeddingGemmaBaseURL     string
	EmbeddingGemmaTimeout     time.Duration
	EmbeddingGemmaDimensions  int
	EmbeddingGemmaConcurrency int
	EmbeddingGemmaDocPrompt   string
	EmbeddingGemmaQueryPrompt string
}

// NewEmbeddingClientFromOptions returns the EmbeddingClient
// the configured Provider asks for, or an error if the
// config is incomplete for that provider.
//
// The "openai" provider requires OpenAIBaseURL and
// OpenAIModel — both fall back to package defaults if
// empty, mirroring the historical behavior of the Ollama
// path. The "embedding_gemma" provider requires
// EmbeddingGemmaBaseURL; there's no model field on the
// wire, so the server picks the model itself.
func NewEmbeddingClientFromOptions(opts EmbeddingClientOptions) (EmbeddingClient, error) {
	provider := opts.Provider
	if provider == "" {
		provider = EmbeddingProviderOpenAI
	}

	switch provider {
	case EmbeddingProviderOpenAI:
		return NewOpenAIEmbeddingClientWithOptions(
			opts.OpenAIBaseURL,
			opts.OpenAIModel,
			opts.OpenAIAPIKey,
			opts.OpenAITimeout,
			opts.OpenAIDimensions,
			opts.OpenAIConcurrency,
			opts.OpenAIDocPrompt,
			opts.OpenAIQueryPrompt,
		), nil

	case EmbeddingProviderEmbeddingGemma:
		if opts.EmbeddingGemmaBaseURL == "" {
			return nil, errors.New("embedding_gemma provider requires embedding_gemma.base_url")
		}
		return NewEmbeddingGemmaClientWithOptions(
			opts.EmbeddingGemmaBaseURL,
			opts.EmbeddingGemmaTimeout,
			opts.EmbeddingGemmaDimensions,
			opts.EmbeddingGemmaConcurrency,
			opts.EmbeddingGemmaDocPrompt,
			opts.EmbeddingGemmaQueryPrompt,
		), nil

	default:
		return nil, fmt.Errorf("unknown embedding provider %q (expected \"openai\" or \"embedding_gemma\")", string(provider))
	}
}
