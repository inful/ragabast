package config

import (
	"errors"
	"time"
)

// OllamaConfig holds configuration for the embedding and chat-completions
// servers. The struct keeps the historical name for backwards-compatible
// config files, but the two URLs are independent: you can point embeddings
// at one server (e.g. Ollama running the nomic-embed-text image) and chat
// at a different OpenAI-compatible server (vLLM, llama.cpp, LM Studio, etc.).
type OllamaConfig struct {
	// BaseURL is the embeddings server endpoint. Speaks the OpenAI
	// /v1/embeddings protocol — supported by Ollama 0.5+, vLLM, llama.cpp
	// --embedding, LM Studio, llama-stack, OpenAI, and any other
	// OpenAI-compatible server. Defaults to a local Ollama instance running
	// the nomic-embed-text image.
	BaseURL string `env:"OLLAMA_BASE_URL" yaml:"base_url"`

	// EmbeddingModel is the model used for embeddings on the embeddings server.
	// Changing this requires updating VectorDB.EmbeddingDimension and
	// re-ingesting all documents (delete data/vectors/ first).
	EmbeddingModel string `env:"OLLAMA_EMBEDDING_MODEL" yaml:"embedding_model"`

	// EmbeddingDimensions is the Matryoshka truncation size. When
	// >0, the embeddings client sends `dimensions: N` with every
	// /v1/embeddings request and warns if the server returns a
	// vector of a different length. Supported by jina v5
	// (32/64/128/256/512/768/1024) and OpenAI text-embedding-3-*
	// (any positive integer). Leave at 0 to disable truncation.
	EmbeddingDimensions int `env:"OLLAMA_EMBEDDING_DIMENSIONS" yaml:"embedding_dimensions"`

	// EmbeddingConcurrency controls the parallel worker pool
	// that GenerateChunkEmbeddings uses for the ingest path.
	// When >1, chunks are split into min(concurrency, n) equal
	// sub-batches and embedded concurrently — useful when the
	// server supports parallel requests (Ollama's
	// OLLAMA_NUM_PARALLEL, vLLM's --max-num-seqs). Default 4
	// matches Ollama's default. Set to 1 to disable the pool.
	EmbeddingConcurrency int `env:"OLLAMA_EMBEDDING_CONCURRENCY" yaml:"embedding_concurrency"`

	// EmbeddingDocPrompt is prepended to every chunk text
	// before the OpenAIEmbeddingClient POSTs to /v1/embeddings
	// on the doc side (GenerateEmbedding, GenerateChunkEmbedding,
	// GenerateChunkEmbeddings). Default empty — preserves the
	// historical raw-text behavior for nomic-embed-text and
	// other prompt-free models. Operators switching to
	// Google's EmbeddingGemma (or another task-prompted
	// model) should set this to Google's recommended
	// `title: none | text:` so the model produces
	// retrieval-optimized document vectors.
	EmbeddingDocPrompt string `env:"OLLAMA_EMBEDDING_DOC_PROMPT" yaml:"embedding_doc_prompt,omitempty"`

	// EmbeddingQueryPrompt is prepended to the user query
	// before the OpenAIEmbeddingClient POSTs to /v1/embeddings
	// on the query side (EmbedQuery). Same default-empty
	// behavior as EmbeddingDocPrompt. For EmbeddingGemma,
	// the recommended value is
	// `task: search result | query:`. The doc and query
	// prompts are independent — operators can tune them
	// separately, including setting one and clearing the
	// other.
	EmbeddingQueryPrompt string `env:"OLLAMA_EMBEDDING_QUERY_PROMPT" yaml:"embedding_query_prompt,omitempty"`

	// ChatBaseURL is the OpenAI-compatible chat completions server endpoint.
	// Defaults to the same local Ollama instance (which exposes
	// /v1/chat/completions from 0.5+), but can point at vLLM, llama.cpp
	// --server, LM Studio, llama-stack, OpenRouter, OpenAI, etc.
	ChatBaseURL string `env:"OLLAMA_CHAT_BASE_URL" yaml:"chat_base_url"`

	// ChatModel is the model name sent to the chat completions server.
	ChatModel string `env:"OLLAMA_CHAT_MODEL" yaml:"chat_model"`

	// APIKey is the default bearer token used for both servers. It is sent
	// as `Authorization: Bearer <key>` to whichever server doesn't have its
	// own override set. Leave empty for unauthenticated local servers.
	APIKey string `env:"OLLAMA_API_KEY" yaml:"api_key"`

	// ChatAPIKey overrides APIKey for the chat completions server only.
	// Useful when the chat provider (e.g. OpenRouter, OpenAI) requires a
	// different token than the embeddings provider.
	ChatAPIKey string `env:"OLLAMA_CHAT_API_KEY" yaml:"chat_api_key"`

	// EmbeddingAPIKey overrides APIKey for the embeddings server only.
	// Useful when the embeddings provider requires a different token than
	// the chat provider.
	EmbeddingAPIKey string `env:"OLLAMA_EMBEDDING_API_KEY" yaml:"embedding_api_key"`

	// Temperature controls LLM sampling. If omitted, the model default is used.
	Temperature *float64 `env:"OLLAMA_TEMPERATURE" yaml:"temperature,omitempty"`

	// Options are model/runtime generation options forwarded to the chat server
	// (e.g. top_p, top_k, num_predict). These are merged into the top-level
	// /v1/chat/completions request body, so any vendor-specific field works.
	Options map[string]any `env:"OLLAMA_OPTIONS_JSON" yaml:"options,omitempty"`

	// Timeout for API calls.
	Timeout time.Duration `env:"OLLAMA_TIMEOUT" yaml:"timeout"`
}

func validateOllamaOptions(opts map[string]any) error {
	if len(opts) == 0 {
		return nil
	}

	getFloat := func(v any) (float64, bool) {
		switch t := v.(type) {
		case float64:
			return t, true
		case float32:
			return float64(t), true
		case int:
			return float64(t), true
		case int64:
			return float64(t), true
		case uint:
			return float64(t), true
		case uint64:
			return float64(t), true
		default:
			return 0, false
		}
	}

	getInt := func(v any) (int64, bool) {
		switch t := v.(type) {
		case int:
			return int64(t), true
		case int64:
			return t, true
		case uint:
			return int64(t), true
		case uint64:
			return int64(t), true
		case float64:
			return int64(t), true
		default:
			return 0, false
		}
	}

	if v, ok := opts["top_p"]; ok {
		if f, ok := getFloat(v); ok {
			if f < 0 || f > 1 {
				return errors.New("ollama.options.top_p must be between 0 and 1")
			}
		}
	}
	if v, ok := opts["min_p"]; ok {
		if f, ok := getFloat(v); ok {
			if f < 0 || f > 1 {
				return errors.New("ollama.options.min_p must be between 0 and 1")
			}
		}
	}
	if v, ok := opts["top_k"]; ok {
		if i, ok := getInt(v); ok {
			if i < 0 {
				return errors.New("ollama.options.top_k must be >= 0")
			}
		}
	}
	if v, ok := opts["num_predict"]; ok {
		if i, ok := getInt(v); ok {
			if i <= 0 {
				return errors.New("ollama.options.num_predict must be > 0")
			}
		}
	}
	if v, ok := opts["num_ctx"]; ok {
		if i, ok := getInt(v); ok {
			if i <= 0 {
				return errors.New("ollama.options.num_ctx must be > 0")
			}
		}
	}

	return nil
}

// EffectiveChatAPIKey returns the bearer token to use for the chat
// completions server. ChatAPIKey wins when set; otherwise the global APIKey
// is used. Returns the empty string when no auth is configured.
func (o *OllamaConfig) EffectiveChatAPIKey() string {
	if o.ChatAPIKey != "" {
		return o.ChatAPIKey
	}
	return o.APIKey
}

// EffectiveEmbeddingAPIKey returns the bearer token to use for the
// embeddings server. EmbeddingAPIKey wins when set; otherwise the global
// APIKey is used. Returns the empty string when no auth is configured.
func (o *OllamaConfig) EffectiveEmbeddingAPIKey() string {
	if o.EmbeddingAPIKey != "" {
		return o.EmbeddingAPIKey
	}
	return o.APIKey
}
