package config

import (
	"fmt"
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

// validateOllamaOptions rejects values that would crash a
// chat-completions server (or vLLM, which returns HTTP 400
// on min_p with speculative decoding). The five knobs
// flagged are the ones with documented floors or ceilings —
// vendor extensions (stop, mirostat, repeat_penalty, ...)
// flow through unmodified.
func validateOllamaOptions(opts map[string]any) error {
	if len(opts) == 0 {
		return nil
	}
	for _, rule := range ollamaOptionChecks {
		v, ok := opts[rule.key]
		if !ok {
			continue
		}
		if err := rule.check(v); err != nil {
			return err
		}
	}
	return nil
}

// ollamaOptionCheck pairs an Options key with a validator
// that runs against the value the operator supplied.
// Parsing errors (wrong type) are deliberately swallowed so
// YAML/JSON int-vs-float ambiguity doesn't reject harmless
// shapes — only true out-of-range values fail.
type ollamaOptionCheck struct {
	key   string
	check func(v any) error
}

// ollamaOptionChecks is the closed set of knobs the
// validator inspects. Adding a new rule means appending one
// constructor call here and (optionally) one new helper;
// nothing in validateOllamaOptions needs to change.
var ollamaOptionChecks = []ollamaOptionCheck{
	ollamaFloatInUnitInterval("top_p"),
	ollamaFloatInUnitInterval("min_p"),
	ollamaIntNonNegative("top_k"),
	ollamaIntStrictlyPositive("num_predict"),
	ollamaIntStrictlyPositive("num_ctx"),
}

// ollamaFloatInUnitInterval returns a check that accepts
// the named option as a float (or any numeric type that
// can losslessly convert to float64) in the closed range
// [0, 1]. Used by top_p and min_p.
func ollamaFloatInUnitInterval(name string) ollamaOptionCheck {
	return ollamaOptionCheck{
		key: name,
		check: func(v any) error {
			f, ok := asFloat64(v)
			if !ok {
				return nil
			}
			if f < 0 || f > 1 {
				return fmt.Errorf("ollama.options.%s must be between 0 and 1", name)
			}
			return nil
		},
	}
}

// ollamaIntNonNegative returns a check that accepts the
// named option as an integer >= 0. Used by top_k.
func ollamaIntNonNegative(name string) ollamaOptionCheck {
	return ollamaOptionCheck{
		key: name,
		check: func(v any) error {
			i, ok := asInt64(v)
			if !ok {
				return nil
			}
			if i < 0 {
				return fmt.Errorf("ollama.options.%s must be >= 0", name)
			}
			return nil
		},
	}
}

// ollamaIntStrictlyPositive returns a check that accepts
// the named option as an integer > 0. Used by num_predict
// and num_ctx — both have a floor of 1.
func ollamaIntStrictlyPositive(name string) ollamaOptionCheck {
	return ollamaOptionCheck{
		key: name,
		check: func(v any) error {
			i, ok := asInt64(v)
			if !ok {
				return nil
			}
			if i <= 0 {
				return fmt.Errorf("ollama.options.%s must be > 0", name)
			}
			return nil
		},
	}
}

// asFloat64 returns v as a float64 when it is one of the
// numeric types any Go decoder (encoding/json, yaml.v3)
// can produce on the hot path: float32, float64, or any of
// the signed and unsigned integer widths. Booleans, strings,
// slices, and maps are not numbers and return ok=false so
// the caller can decide whether to pass them through.
func asFloat64(v any) (float64, bool) {
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

// asInt64 is the integer counterpart to asFloat64. It
// returns v as an int64 when the source type is one of
// the integer or float64 shapes a Go decoder can emit.
// Float64 is accepted because JSON's number type covers
// both: top_k: 20 (int) and top_k: 20.0 (float) parse to
// different Go types depending on the surface, and the
// validator must accept either.
func asInt64(v any) (int64, bool) {
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
