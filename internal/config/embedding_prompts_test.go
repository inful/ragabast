package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplyEnvOverrides_EmbeddingDocPrompt pins the
// env-var wiring for the embedding-gemma optimization:
// OLLAMA_EMBEDDING_DOC_PROMPT populates OllamaConfig.EmbeddingDocPrompt.
// The default value matches Google's recommended
// `title: none | text:` — operators who want anything
// else override via env or YAML.
func TestApplyEnvOverrides_EmbeddingDocPrompt(t *testing.T) {
	t.Setenv("OLLAMA_EMBEDDING_DOC_PROMPT", "title: none | text:")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, "title: none | text:", cfg.Ollama.EmbeddingDocPrompt,
		"OLLAMA_EMBEDDING_DOC_PROMPT must populate OllamaConfig.EmbeddingDocPrompt")
}

// TestApplyEnvOverrides_EmbeddingDocPrompt_EmptyClearsField
// pins the explicit-empty behavior: setting the env
// var to an empty string clears the field (operators
// can disable the prompt without unsetting the variable).
func TestApplyEnvOverrides_EmbeddingDocPrompt_EmptyClearsField(t *testing.T) {
	// Pre-populate so we can prove the override actually overwrites.
	cfg := DefaultConfig()
	cfg.Ollama.EmbeddingDocPrompt = "stale value"

	t.Setenv("OLLAMA_EMBEDDING_DOC_PROMPT", "")
	cfg.ApplyEnvOverrides()
	require.Equal(t, "", cfg.Ollama.EmbeddingDocPrompt,
		"empty env var must overwrite the field with \"\"")
}

// TestApplyEnvOverrides_EmbeddingQueryPrompt mirrors
// the doc-prompt test for the query side. Together they
// prove both knobs are wired through env vars.
func TestApplyEnvOverrides_EmbeddingQueryPrompt(t *testing.T) {
	t.Setenv("OLLAMA_EMBEDDING_QUERY_PROMPT", "task: search result | query:")
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Equal(t, "task: search result | query:",
		cfg.Ollama.EmbeddingQueryPrompt,
		"OLLAMA_EMBEDDING_QUERY_PROMPT must populate OllamaConfig.EmbeddingQueryPrompt")
}

// TestApplyEnvOverrides_EmbeddingPrompts_DefaultsAreEmpty
// pins the unconfigured behavior: when neither env var
// is set, both prompt fields stay empty (no prepend,
// preserves raw-text behavior for nomic-embed-text
// and other models without prompt requirements).
func TestApplyEnvOverrides_EmbeddingPrompts_DefaultsAreEmpty(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	require.Empty(t, cfg.Ollama.EmbeddingDocPrompt,
		"docPrompt must default to empty so non-embedding-gemma models keep working")
	require.Empty(t, cfg.Ollama.EmbeddingQueryPrompt,
		"queryPrompt must default to empty so non-embedding-gemma models keep working")
}

// TestDefaultConfig_EmbeddingPrompts_Empty pins the
// direct-from-DefaultConfig behavior (env override path
// not exercised). Same intent — the default is no
// prompt — but at a different config layer so a future
// refactor that wires a default doesn't slip through.
func TestDefaultConfig_EmbeddingPrompts_Empty(t *testing.T) {
	cfg := DefaultConfig()
	require.Empty(t, cfg.Ollama.EmbeddingDocPrompt)
	require.Empty(t, cfg.Ollama.EmbeddingQueryPrompt)
}
