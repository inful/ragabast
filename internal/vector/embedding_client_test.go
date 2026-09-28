package vector

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewEmbeddingClientFromOptions_OpenAIDefault pins the
// historical behavior: empty Provider falls back to the
// OpenAI-compat client. Existing operators see no change.
func TestNewEmbeddingClientFromOptions_OpenAIDefault(t *testing.T) {
	t.Parallel()

	client, err := NewEmbeddingClientFromOptions(EmbeddingClientOptions{
		OpenAIBaseURL: "http://example.com",
		OpenAIModel:   "m",
		OpenAITimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	_, ok := client.(*OpenAIEmbeddingClient)
	require.True(t, ok, "default provider must return *OpenAIEmbeddingClient")
}

// TestNewEmbeddingClientFromOptions_OpenAIExplicit pins the
// "openai" string as a valid provider. Same concrete type
// as the empty default — the string is just an alias.
func TestNewEmbeddingClientFromOptions_OpenAIExplicit(t *testing.T) {
	t.Parallel()

	client, err := NewEmbeddingClientFromOptions(EmbeddingClientOptions{
		Provider:      EmbeddingProviderOpenAI,
		OpenAIBaseURL: "http://example.com",
		OpenAIModel:   "m",
		OpenAITimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	_, ok := client.(*OpenAIEmbeddingClient)
	require.True(t, ok)
}

// TestNewEmbeddingClientFromOptions_EmbeddingGemma pins the
// new path: "embedding_gemma" returns the new client type,
// not the OpenAI one. An operator who sets EMBEDDING_PROVIDER
// to "embedding_gemma" must get the right wire shape at
// runtime.
func TestNewEmbeddingClientFromOptions_EmbeddingGemma(t *testing.T) {
	t.Parallel()

	client, err := NewEmbeddingClientFromOptions(EmbeddingClientOptions{
		Provider:                EmbeddingProviderEmbeddingGemma,
		EmbeddingGemmaBaseURL:   "http://example.com",
		EmbeddingGemmaTimeout:   5 * time.Second,
		EmbeddingGemmaDocPrompt: "title: none | text:",
	})
	require.NoError(t, err)
	_, ok := client.(*EmbeddingGemmaClient)
	require.True(t, ok, "embedding_gemma provider must return *EmbeddingGemmaClient")
}

// TestNewEmbeddingClientFromOptions_EmbeddingGemmaMissingBaseURL
// pins the validation guard: switching to the embedding_gemma
// provider without a base URL is a config error, not a silent
// fallback to localhost.
func TestNewEmbeddingClientFromOptions_EmbeddingGemmaMissingBaseURL(t *testing.T) {
	t.Parallel()

	_, err := NewEmbeddingClientFromOptions(EmbeddingClientOptions{
		Provider: EmbeddingProviderEmbeddingGemma,
		// EmbeddingGemmaBaseURL intentionally empty
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "base_url")
}

// TestNewEmbeddingClientFromOptions_UnknownProvider pins
// the failure mode for a typo in the provider string: an
// explicit error rather than silently picking the default.
// Catches things like `embedding_gema` or `openai_compat`.
func TestNewEmbeddingClientFromOptions_UnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := NewEmbeddingClientFromOptions(EmbeddingClientOptions{
		Provider:      "nope",
		OpenAIBaseURL: "http://x",
		OpenAIModel:   "m",
		OpenAITimeout: 5 * time.Second,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nope")
}

// TestNewEmbeddingClientFromOptions_SatisfiesInterface is a
// compile-time assertion that the factory returns values
// that satisfy EmbeddingClient. If a future client drops
// a method, this test won't compile.
func TestNewEmbeddingClientFromOptions_SatisfiesInterface(t *testing.T) {
	t.Parallel()

	var c EmbeddingClient
	client, err := NewEmbeddingClientFromOptions(EmbeddingClientOptions{
		OpenAIBaseURL: "http://x",
		OpenAIModel:   "m",
		OpenAITimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	c = client
	_ = c
}
