package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSaveConfig_WritesFileWithRestrictivePermissions pins
// the M-1 fix: config files written by `ragabast config init`
// may carry bearer tokens (server.auth_token) and LLM API
// keys (ollama.api_key). The default umask on a typical
// Linux box is 022, so a WriteFile with mode 0o644 produces
// a world-readable file — exposing the secrets to every
// local user. The fix: SaveConfig writes the file with
// 0o600 (owner read/write only), regardless of umask, so
// the operator does not have to remember to chmod after
// generating the config.
//
// Test asserts: after SaveConfig, the file's effective
// permission bits are exactly 0o600.
func TestSaveConfig_WritesFileWithRestrictivePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	cfg := DefaultConfig()
	cfg.Server.AuthToken = "secret-bearer-token"
	cfg.Ollama.APIKey = "sk-secret-llm-key"

	require.NoError(t, cfg.SaveConfig(path))

	info, err := os.Stat(path)
	require.NoError(t, err)

	// Mask off the high bits (file type etc.) so we are only
	// comparing the permission bits. 0o600 = owner read+write
	// only, no group or other access.
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"config files containing tokens must be owner-only (0o600), got %v",
		info.Mode().Perm())
}

// TestSaveConfig_OverwritesExistingFile pins that SaveConfig
// (called from `ragabast config init`) properly handles the
// overwrite case while still applying 0o600. Without this,
// a second run of `config init --force` would inherit the
// existing file's permissions.
func TestSaveConfig_OverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	// Pre-create the file with overly permissive perms.
	require.NoError(t, os.WriteFile(path, []byte("# stale"), 0o644))

	cfg := DefaultConfig()
	require.NoError(t, cfg.SaveConfig(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"overwrite must re-apply 0o600 even when the pre-existing file was world-readable")
}
