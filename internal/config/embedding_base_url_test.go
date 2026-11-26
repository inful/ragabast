package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidate_GemmaBaseURLPointingAtOpenAIEndpoint pins the
// mistake that a real deployment hit: the OpenAI-compatible
// embeddings endpoint pasted into embedding_gemma.base_url. The
// client appends /v2/embed, so the server was asked for
// ".../v1/embeddings/v2/embed" and every ingest failed with a
// 404. The config error names the mistake instead.
func TestValidate_GemmaBaseURLPointingAtOpenAIEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmbeddingProvider = "embedding_gemma"
	cfg.EmbeddingGemma.BaseURL = "https://llm-endpoint.example.com/v1/embeddings"

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "embedding_gemma.base_url")
	require.Contains(t, err.Error(), "/v2/embed",
		"the error must say which path the provider speaks")
	require.Contains(t, err.Error(), "ollama.base_url",
		"the error must point at the provider that fits an OpenAI-compatible server")
}

// TestValidate_OllamaBaseURLPointingAtEmbeddingsEndpoint pins the
// mirror mistake: the embeddings endpoint pasted into
// ollama.base_url. The client appends /v1/embeddings, so the server
// was asked for ".../embeddings/v1/embeddings".
func TestValidate_OllamaBaseURLPointingAtEmbeddingsEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.BaseURL = "https://llm-endpoint.example.com/v1/embeddings"

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ollama.base_url")
	require.Contains(t, err.Error(), "/v1/embeddings")
}

// TestValidate_ServerRootBaseURLsAreAccepted pins that the guard
// does not fire on a server root, which is what the clients want.
func TestValidate_ServerRootBaseURLsAreAccepted(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.BaseURL = "https://llm-endpoint.example.com"
	require.NoError(t, cfg.Validate())

	cfg = DefaultConfig()
	cfg.Ollama.BaseURL = "https://llm-endpoint.example.com/v1"
	require.NoError(t, cfg.Validate(), "a /v1 root is normalized by the client")

	cfg = DefaultConfig()
	cfg.EmbeddingProvider = "embedding_gemma"
	cfg.EmbeddingGemma.BaseURL = "https://embedder.example.com"
	require.NoError(t, cfg.Validate())
}
