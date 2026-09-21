package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_UsesConfigYMLWhenPresent(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfgPath := filepath.Join(tmp, DefaultConfigYML)
	cfg := DefaultConfig()
	cfg.Server.Port = 9090
	require.NoError(t, cfg.SaveConfig(cfgPath))

	loaded, err := Load("")
	require.NoError(t, err)
	require.Equal(t, 9090, loaded.Server.Port)
}

func TestLoad_RespectsExplicitPath(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfg := DefaultConfig()
	cfg.Ollama.BaseURL = "http://example.invalid:11434"

	cfgPath := filepath.Join(tmp, "custom.yml")
	require.NoError(t, cfg.SaveConfig(cfgPath))

	loaded, err := Load(cfgPath)
	require.NoError(t, err)
	require.Equal(t, "http://example.invalid:11434", loaded.Ollama.BaseURL)
}

func TestLoad_FallsBackToDefaultsWhenNoFile(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	loaded, err := Load("")
	require.NoError(t, err)
	require.Equal(t, 8080, loaded.Server.Port)
}

func TestLoad_AppliesOllamaTemperatureEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	t.Setenv("OLLAMA_TEMPERATURE", "0.35")
	loaded, err := Load("")
	require.NoError(t, err)
	require.NotNil(t, loaded.Ollama.Temperature)
	require.InDelta(t, 0.35, *loaded.Ollama.Temperature, 1e-9)
}

func TestValidate_RejectsOutOfRangeOllamaTemperature(t *testing.T) {
	cfg := DefaultConfig()
	v := 2.5
	cfg.Ollama.Temperature = &v
	require.Error(t, cfg.Validate())
}

func TestLoad_AppliesOllamaOptionsJSONEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	t.Setenv("OLLAMA_OPTIONS_JSON", `{"top_k":12,"top_p":0.8,"stop":["\n\nLinks:"]}`)
	loaded, err := Load("")
	require.NoError(t, err)
	require.NotNil(t, loaded.Ollama.Options)

	topK, ok := loaded.Ollama.Options["top_k"].(float64)
	require.True(t, ok)
	require.InDelta(t, 12, topK, 1e-9)

	topP, ok := loaded.Ollama.Options["top_p"].(float64)
	require.True(t, ok)
	require.InDelta(t, 0.8, topP, 1e-9)

	stop, ok := loaded.Ollama.Options["stop"].([]any)
	require.True(t, ok)
	require.Len(t, stop, 1)
	require.Equal(t, "\n\nLinks:", stop[0])
}

func TestValidate_RejectsOutOfRangeOllamaTopP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.Options = map[string]any{"top_p": 1.2}
	require.Error(t, cfg.Validate())
}

// TestValidate_AcceptsEmptyDocbuilderBaseURL pins the
// "no permalinks" mode: empty URL is the default and must
// keep Validate() happy so local single-user installs
// without docbuilder don't trip on a new requirement.
func TestValidate_AcceptsEmptyDocbuilderBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = ""
	require.NoError(t, cfg.Validate())
}

// TestValidate_AcceptsHTTPAndHTTPSDocbuilderBaseURL pins
// the only two schemes we accept. Anything else (ftp, file,
// javascript, etc.) is rejected — see the Rejects tests
// below.
func TestValidate_AcceptsHTTPAndHTTPSDocbuilderBaseURL(t *testing.T) {
	for _, url := range []string{
		"http://docs.example.com",
		"https://docs.example.com",
		"https://docs.example.com/",
		"https://docs.example.com:8443",
		"https://docs.example.com:8443/",
		"https://docs.example.com/docs",
		"https://docs.example.com/docs/",
	} {
		t.Run(url, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Ragabast.DocbuilderBaseURL = url
			require.NoError(t, cfg.Validate(), "should accept %q", url)
		})
	}
}

// TestValidate_AcceptsIPv6DocbuilderBaseURL: IPv6 hosts
// use bracket notation and url.Parse handles them
// correctly, so the validator must too.
func TestValidate_AcceptsIPv6DocbuilderBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "http://[::1]:8080/"
	require.NoError(t, cfg.Validate())
}

// TestValidate_TrimsWhitespaceAroundDocbuilderBaseURL:
// operators routinely paste URLs from chat messages and
// end up with a stray space. We trim and accept, AND we
// must persist the trimmed value so the synthesized
// permalinks don't have the stray space either.
func TestValidate_TrimsWhitespaceAroundDocbuilderBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "  https://docs.example.com/  "
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "https://docs.example.com/", cfg.Ragabast.DocbuilderBaseURL,
		"trimmed URL must be persisted")
}

// TestValidate_RejectsTypoDocbuilderBaseURL: a scheme
// typo is the canonical config bug this issue exists to
// catch. Without validation, the typo silently produces
// broken permalinks for every search result.
func TestValidate_RejectsTypoDocbuilderBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "htttp://docs.example.com"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docbuilder_base_url")
	assert.Contains(t, err.Error(), "htttp://")
}

// TestValidate_RejectsMissingHostDocbuilderBaseURL:
// "http://" alone parses fine under url.Parse but the
// Host field is empty, so it must be rejected.
func TestValidate_RejectsMissingHostDocbuilderBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "http://"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docbuilder_base_url")
}

// TestValidate_RejectsGarbageDocbuilderBaseURL: anything
// url.Parse can't even interpret must be rejected.
func TestValidate_RejectsGarbageDocbuilderBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "not a url at all"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docbuilder_base_url")
}

// TestValidate_RejectsNonHTTPSchemeDocbuilderBaseURL:
// scheme must be exactly http or https. ftp://, file://,
// javascript:, etc. all fall under this rule. The threat
// model is broken permalinks — a javascript: scheme would
// be worse than broken.
func TestValidate_RejectsNonHTTPSchemeDocbuilderBaseURL(t *testing.T) {
	for _, url := range []string{
		"ftp://docs.example.com",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"ws://docs.example.com",
	} {
		t.Run(url, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Ragabast.DocbuilderBaseURL = url
			require.Error(t, cfg.Validate(), "should reject %q", url)
		})
	}
}

// TestValidate_RejectsWhitespaceOnlyDocbuilderBaseURL:
// trimming an all-whitespace string yields empty, which
// is the "no permalinks" mode — and empty is already
// accepted. So this case must be accepted after trim.
func TestValidate_RejectsWhitespaceOnlyDocbuilderBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "   "
	require.NoError(t, cfg.Validate(), "whitespace-only trims to empty (valid)")
	assert.Equal(t, "", cfg.Ragabast.DocbuilderBaseURL,
		"trimmed value must be persisted as empty")
}

func TestEffectiveAPIKeys_DefaultsToGlobal(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.APIKey = "global-key"

	require.Equal(t, "global-key", cfg.Ollama.EffectiveChatAPIKey())
	require.Equal(t, "global-key", cfg.Ollama.EffectiveEmbeddingAPIKey())
}

func TestEffectiveAPIKeys_PerServerOverrides(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.APIKey = "global-key"
	cfg.Ollama.ChatAPIKey = "chat-only-key"
	cfg.Ollama.EmbeddingAPIKey = "embed-only-key"

	require.Equal(t, "chat-only-key", cfg.Ollama.EffectiveChatAPIKey())
	require.Equal(t, "embed-only-key", cfg.Ollama.EffectiveEmbeddingAPIKey())
}

func TestEffectiveAPIKeys_OnlyChatOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.APIKey = "global-key"
	cfg.Ollama.ChatAPIKey = "chat-only-key"

	require.Equal(t, "chat-only-key", cfg.Ollama.EffectiveChatAPIKey())
	require.Equal(t, "global-key", cfg.Ollama.EffectiveEmbeddingAPIKey())
}

func TestEffectiveAPIKeys_OnlyEmbeddingOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.APIKey = "global-key"
	cfg.Ollama.EmbeddingAPIKey = "embed-only-key"

	require.Equal(t, "global-key", cfg.Ollama.EffectiveChatAPIKey())
	require.Equal(t, "embed-only-key", cfg.Ollama.EffectiveEmbeddingAPIKey())
}

func TestEffectiveAPIKeys_NoAuthReturnsEmpty(t *testing.T) {
	cfg := DefaultConfig()

	require.Empty(t, cfg.Ollama.EffectiveChatAPIKey())
	require.Empty(t, cfg.Ollama.EffectiveEmbeddingAPIKey())
}

func TestLoad_AppliesPerServerAPIKeyEnvOverrides(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	t.Setenv("OLLAMA_API_KEY", "global-key")
	t.Setenv("OLLAMA_CHAT_API_KEY", "chat-key")
	t.Setenv("OLLAMA_EMBEDDING_API_KEY", "embed-key")

	loaded, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "global-key", loaded.Ollama.APIKey)
	require.Equal(t, "chat-key", loaded.Ollama.ChatAPIKey)
	require.Equal(t, "embed-key", loaded.Ollama.EmbeddingAPIKey)
	require.Equal(t, "chat-key", loaded.Ollama.EffectiveChatAPIKey())
	require.Equal(t, "embed-key", loaded.Ollama.EffectiveEmbeddingAPIKey())
}

func TestLoad_AppliesLogChatRequestsEnvOverride(t *testing.T) {
	t.Setenv("RAGABAST_LOG_CHAT_REQUESTS", "true")
	loaded, err := Load("")
	require.NoError(t, err)
	require.True(t, loaded.Ragabast.LogChatRequests,
		"RAGABAST_LOG_CHAT_REQUESTS=true must enable LogChatRequests")

	t.Setenv("RAGABAST_LOG_CHAT_REQUESTS", "false")
	loaded, err = Load("")
	require.NoError(t, err)
	require.False(t, loaded.Ragabast.LogChatRequests,
		"RAGABAST_LOG_CHAT_REQUESTS=false must disable LogChatRequests")

	t.Setenv("RAGABAST_LOG_CHAT_REQUESTS", "yes")
	loaded, err = Load("")
	require.NoError(t, err)
	require.True(t, loaded.Ragabast.LogChatRequests,
		"truthy spellings ('yes') must enable LogChatRequests")
}

func TestLoad_AppliesDocbuilderBaseURLEnvOverride(t *testing.T) {
	t.Setenv("RAGABAST_DOCBUILDER_BASE_URL", "https://docs.example.com")
	loaded, err := Load("")
	require.NoError(t, err)
	require.Equal(t, "https://docs.example.com", loaded.Ragabast.DocbuilderBaseURL)
}
