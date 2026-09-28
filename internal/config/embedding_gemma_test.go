package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplyEnvOverrides_EmbeddingProvider pins the env
// wiring for the provider selector: EMBEDDING_PROVIDER
// switches between the OpenAI-compat path and the
// EmbeddingGemma /v2/embed path. Empty / unset keeps the
// historical OpenAI-compat default.
func TestApplyEnvOverrides_EmbeddingProvider(t *testing.T) {
	t.Setenv("EMBEDDING_PROVIDER", "embedding_gemma")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, "embedding_gemma", cfg.EmbeddingProvider,
		"EMBEDDING_PROVIDER must populate Config.EmbeddingProvider")
}

// TestApplyEnvOverrides_EmbeddingProviderDefaultsEmpty pins
// the historical behavior: no env var set means no
// provider override, and the factory defaults to "openai".
// Existing operators see no change.
func TestApplyEnvOverrides_EmbeddingProviderDefaultsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Empty(t, cfg.EmbeddingProvider,
		"missing EMBEDDING_PROVIDER must leave the field empty")
}

// TestApplyEnvOverrides_EmbeddingGemmaBaseURL pins the
// /v2/embed server URL: EMBEDDING_GEMMA_BASE_URL populates
// EmbeddingGemmaConfig.BaseURL.
func TestApplyEnvOverrides_EmbeddingGemmaBaseURL(t *testing.T) {
	t.Setenv("EMBEDDING_GEMMA_BASE_URL", "https://embedder.example.com")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, "https://embedder.example.com",
		cfg.EmbeddingGemma.BaseURL,
		"EMBEDDING_GEMMA_BASE_URL must populate EmbeddingGemmaConfig.BaseURL")
}

// TestApplyEnvOverrides_EmbeddingGemmaDocPrompt pins the
// doc-prompt wiring for the EmbeddingGemma path. Same
// behavior as the Ollama-side OLLAMA_EMBEDDING_DOC_PROMPT
// but under a separate env namespace so operators can
// switch providers without losing the prompt config.
func TestApplyEnvOverrides_EmbeddingGemmaDocPrompt(t *testing.T) {
	t.Setenv("EMBEDDING_GEMMA_DOC_PROMPT", "title: none | text:")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, "title: none | text:",
		cfg.EmbeddingGemma.EmbeddingDocPrompt,
		"EMBEDDING_GEMMA_DOC_PROMPT must populate EmbeddingGemmaConfig.EmbeddingDocPrompt")
}

// TestApplyEnvOverrides_EmbeddingGemmaQueryPrompt pins
// the query-prompt wiring for the EmbeddingGemma path.
func TestApplyEnvOverrides_EmbeddingGemmaQueryPrompt(t *testing.T) {
	t.Setenv("EMBEDDING_GEMMA_QUERY_PROMPT", "task: search result | query:")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, "task: search result | query:",
		cfg.EmbeddingGemma.EmbeddingQueryPrompt,
		"EMBEDDING_GEMMA_QUERY_PROMPT must populate EmbeddingGemmaConfig.EmbeddingQueryPrompt")
}

// TestApplyEnvOverrides_EmbeddingGemmaDimensions pins the
// dimension check knob: EMBEDDING_GEMMA_DIMENSIONS
// populates EmbeddingGemmaConfig.EmbeddingDimensions.
func TestApplyEnvOverrides_EmbeddingGemmaDimensions(t *testing.T) {
	t.Setenv("EMBEDDING_GEMMA_DIMENSIONS", "768")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, 768, cfg.EmbeddingGemma.EmbeddingDimensions,
		"EMBEDDING_GEMMA_DIMENSIONS must populate EmbeddingGemmaConfig.EmbeddingDimensions")
}

// TestApplyEnvOverrides_EmbeddingGemmaConcurrency pins
// the worker-pool knob for the EmbeddingGemma path.
func TestApplyEnvOverrides_EmbeddingGemmaConcurrency(t *testing.T) {
	t.Setenv("EMBEDDING_GEMMA_CONCURRENCY", "8")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, 8, cfg.EmbeddingGemma.EmbeddingConcurrency,
		"EMBEDDING_GEMMA_CONCURRENCY must populate EmbeddingGemmaConfig.EmbeddingConcurrency")
}

// TestValidate_EmbeddingGemmaProvider_RequiresBaseURL pins
// the validation guard: switching to the EmbeddingGemma
// provider without setting a base URL is a config error.
// Operators hitting this see a clear "you picked the
// provider but didn't tell me where to talk to" message
// rather than a silent fallback to localhost.
func TestValidate_EmbeddingGemmaProvider_RequiresBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmbeddingProvider = "embedding_gemma"
	// EmbeddingGemma.BaseURL intentionally empty.

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "embedding_gemma.base_url")
}

// TestValidate_EmbeddingGemmaProvider_OKWithBaseURL pins
// the happy path: EmbeddingGemma provider with a valid
// base URL passes Validate.
func TestValidate_EmbeddingGemmaProvider_OKWithBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmbeddingProvider = "embedding_gemma"
	cfg.EmbeddingGemma.BaseURL = "https://embedder.example.com"

	require.NoError(t, cfg.Validate())
}

// TestValidate_UnknownProviderRejected pins the failure
// mode for a typo in the provider string. Catches things
// like `embedding_gema` or `openai_compat`.
func TestValidate_UnknownProviderRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmbeddingProvider = "openai_compat" // not a real provider

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "openai_compat")
}

// TestValidate_DefaultProviderStillPasses pins the
// historical behavior: empty EmbeddingProvider (the
// default) passes Validate unchanged. Existing operators
// without the new field in their config see no
// regression.
func TestValidate_DefaultProviderStillPasses(t *testing.T) {
	cfg := DefaultConfig()
	require.Empty(t, cfg.EmbeddingProvider)
	require.NoError(t, cfg.Validate())
}

// TestDefaultConfig_EmbeddingGemmaFieldsEmpty pins the
// default state of EmbeddingGemmaConfig. Default config
// must not pre-populate anything in the EmbeddingGemma
// block — that block is consulted only when the operator
// explicitly switches providers.
func TestDefaultConfig_EmbeddingGemmaFieldsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	require.Empty(t, cfg.EmbeddingGemma.BaseURL,
		"default config must not pre-fill EmbeddingGemma.BaseURL")
	require.Zero(t, cfg.EmbeddingGemma.EmbeddingDimensions)
	require.Zero(t, cfg.EmbeddingGemma.EmbeddingConcurrency)
	require.Empty(t, cfg.EmbeddingGemma.EmbeddingDocPrompt)
	require.Empty(t, cfg.EmbeddingGemma.EmbeddingQueryPrompt)
	require.Zero(t, cfg.EmbeddingGemma.Timeout)
}
