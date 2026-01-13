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
