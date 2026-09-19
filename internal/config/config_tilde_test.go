package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadConfig_TildeExpandsToHome pins the contract that a
// leading "~/" in a config path is expanded to the user's home
// directory. Go's os.ReadFile does NOT do this natively, so a
// user who sets --config ~/.ragabast.yml hits a confusing "no
// such file or directory" error otherwise. This matches the
// behavior of kubectl, npm, and most other CLIs.
//
// The test sets HOME to a fresh temp dir, writes a config at
// ~/config.yml inside it, and asks LoadConfig to load via the
// tilde path. Success means the expansion happened.
func TestLoadConfig_TildeExpandsToHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Some platforms also key on USERPROFILE; setting both keeps
	// os.UserHomeDir() happy across Linux, macOS, and the
	// handful of CI environments that mimic Windows lookup.
	t.Setenv("USERPROFILE", tmp)

	cfg := DefaultConfig()
	cfg.Server.Port = 9091
	require.NoError(t, cfg.SaveConfig(filepath.Join(tmp, "config.yml")))

	loaded, err := LoadConfig("~/config.yml")
	require.NoError(t, err, "~/config.yml must resolve to HOME/config.yml")
	require.Equal(t, 9091, loaded.Server.Port)
}

// TestLoadConfig_TildeWithSubdir pins that ~/path/with/slashes
// resolves to HOME/path/with/slashes (not just HOME).
func TestLoadConfig_TildeWithSubdir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	subdir := filepath.Join(tmp, "sub", "dir")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	cfg := DefaultConfig()
	cfg.Server.Port = 9092
	require.NoError(t, cfg.SaveConfig(filepath.Join(subdir, "x.yml")))

	loaded, err := LoadConfig("~/sub/dir/x.yml")
	require.NoError(t, err)
	require.Equal(t, 9092, loaded.Server.Port)
}

// TestLoadConfig_AbsolutePathUntouched pins that an absolute path
// (no tilde) passes through unchanged. The tilde-expansion code
// must not mutate paths that don't start with "~".
func TestLoadConfig_AbsolutePathUntouched(t *testing.T) {
	tmp := t.TempDir()
	cfg := DefaultConfig()
	cfg.Server.Port = 9093
	absPath := filepath.Join(tmp, "abs.yml")
	require.NoError(t, cfg.SaveConfig(absPath))

	loaded, err := LoadConfig(absPath)
	require.NoError(t, err)
	require.Equal(t, 9093, loaded.Server.Port)
}

// TestLoadConfig_RelativePathUntouched pins that a relative
// path (no tilde) passes through unchanged too.
func TestLoadConfig_RelativePathUntouched(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfg := DefaultConfig()
	cfg.Server.Port = 9094
	require.NoError(t, cfg.SaveConfig(filepath.Join(tmp, "rel.yml")))

	loaded, err := LoadConfig("rel.yml")
	require.NoError(t, err)
	require.Equal(t, 9094, loaded.Server.Port)
}
