package config

import (
	"path/filepath"
	"testing"

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
