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

// TestApplyEnvOverrides_PathFieldsTildeExpanded pins that a
// leading "~/" in any path field is expanded to the user's
// home directory, regardless of whether the value arrived via
// YAML or via env var. The historical code only expanded the
// config FILE path (the argument to --config / LoadConfig);
// every other path field — paths.data_dir, paths.templates_dir,
// vectordb.persistence_dir, vectordb.keyword_index_dir,
// server.async_ingest_queue_dir — stored the literal "~/"
// and failed downstream when the OS tried to open the path.
//
// This test asserts the contract for env-var values. The
// YAML-value path is exercised by TestLoadConfig_*_TildeInYAML
// below; both go through the same post-processing step in
// ApplyEnvOverrides, so the env-var cases are the workhorse.
func TestApplyEnvOverrides_PathFieldsTildeExpanded(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	cases := []struct {
		name     string
		envKey   string
		envValue string
		// read returns the field to assert on so the cases
		// don't have to repeat field-access boilerplate.
		read func(*Config) string
		// sub is the sub-path appended after HOME so we can
		// tell two fields apart even though they share the
		// same prefix.
		sub string
	}{
		{
			name:     "DATA_DIR expands ~",
			envKey:   "DATA_DIR",
			envValue: "~/ragabast-data",
			read:     func(c *Config) string { return c.Paths.DataDir },
			sub:      "ragabast-data",
		},
		{
			name:     "TEMPLATES_DIR expands ~",
			envKey:   "TEMPLATES_DIR",
			envValue: "~/templates",
			read:     func(c *Config) string { return c.Paths.TemplatesDir },
			sub:      "templates",
		},
		{
			name:     "VECTOR_DB_DIR expands ~",
			envKey:   "VECTOR_DB_DIR",
			envValue: "~/vectors",
			read:     func(c *Config) string { return c.VectorDB.PersistenceDir },
			sub:      "vectors",
		},
		{
			name:     "VECTOR_DB_KEYWORD_INDEX_DIR expands ~",
			envKey:   "VECTOR_DB_KEYWORD_INDEX_DIR",
			envValue: "~/keyword",
			read:     func(c *Config) string { return c.VectorDB.KeywordIndexDir },
			sub:      "keyword",
		},
		{
			name:     "SERVER_ASYNC_INGEST_QUEUE_DIR expands ~",
			envKey:   "SERVER_ASYNC_INGEST_QUEUE_DIR",
			envValue: "~/queue",
			read:     func(c *Config) string { return c.Server.AsyncIngestQueueDir },
			sub:      "queue",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			// ApplyEnvOverrides reads the env directly, so the
			// test must t.Setenv the variable BEFORE the call.
			t.Setenv(tc.envKey, tc.envValue)

			cfg.ApplyEnvOverrides()

			want := filepath.Join(tmp, tc.sub)
			require.Equal(t, want, tc.read(cfg),
				"%s=%q must resolve to HOME/%s after ApplyEnvOverrides",
				tc.envKey, tc.envValue, tc.sub)
		})
	}
}

// TestApplyEnvOverrides_PathFieldsNonTouchUntouched pins that
// absolute and relative path values are NOT mangled by the
// tilde-expansion step. resolvePath only touches strings that
// start with "~"; everything else is passed through. This is
// the complement of TestApplyEnvOverrides_PathFieldsTildeExpanded.
func TestApplyEnvOverrides_PathFieldsNonTouchUntouched(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	cases := []struct {
		name     string
		envKey   string
		envValue string
		read     func(*Config) string
	}{
		{name: "DATA_DIR absolute", envKey: "DATA_DIR", envValue: "/var/lib/ragabast", read: func(c *Config) string { return c.Paths.DataDir }},
		{name: "DATA_DIR relative", envKey: "DATA_DIR", envValue: "data/sub", read: func(c *Config) string { return c.Paths.DataDir }},
		{name: "VECTOR_DB_DIR absolute", envKey: "VECTOR_DB_DIR", envValue: "/srv/vectors", read: func(c *Config) string { return c.VectorDB.PersistenceDir }},
		{name: "VECTOR_DB_DIR relative", envKey: "VECTOR_DB_DIR", envValue: "data/vectors", read: func(c *Config) string { return c.VectorDB.PersistenceDir }},
		{name: "TEMPLATES_DIR absolute", envKey: "TEMPLATES_DIR", envValue: "/etc/ragabast/templates", read: func(c *Config) string { return c.Paths.TemplatesDir }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			t.Setenv(tc.envKey, tc.envValue)
			cfg.ApplyEnvOverrides()
			require.Equal(t, tc.envValue, tc.read(cfg),
				"%s=%q must pass through resolvePath unchanged", tc.envKey, tc.envValue)
		})
	}
}

// TestApplyEnvOverrides_PathFieldsEmptyUntouched pins that an
// empty path field stays empty after ApplyEnvOverrides. The
// tilde step must not produce "/" or the user's home dir
// when the value is unset (the empty string is the
// "use the default" signal for every path field).
//
// DefaultConfig() pre-fills paths.data_dir and
// vectordb.persistence_dir with cwd-relative defaults; the
// other three fields are empty by default. The test starts
// from a bare Config (not DefaultConfig) so the assertions
// reflect the actual "field is empty" invariant rather than
// overlapping with the defaults — DefaultConfig's defaults
// are exercised by the absolute/relative/tilde tests above
// and by the wider config tests.
func TestApplyEnvOverrides_PathFieldsEmptyUntouched(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	// Make sure none of the path env vars are set.
	t.Setenv("DATA_DIR", "")
	t.Setenv("TEMPLATES_DIR", "")
	t.Setenv("VECTOR_DB_DIR", "")
	t.Setenv("VECTOR_DB_KEYWORD_INDEX_DIR", "")
	t.Setenv("SERVER_ASYNC_INGEST_QUEUE_DIR", "")

	cfg := &Config{}
	cfg.ApplyEnvOverrides()

	require.Empty(t, cfg.Paths.DataDir,
		"DATA_DIR empty must stay empty (no HOME/ substitution)")
	require.Empty(t, cfg.Paths.TemplatesDir,
		"TEMPLATES_DIR empty must stay empty")
	require.Empty(t, cfg.VectorDB.PersistenceDir,
		"VECTOR_DB_DIR empty must stay empty")
	require.Empty(t, cfg.VectorDB.KeywordIndexDir,
		"VECTOR_DB_KEYWORD_INDEX_DIR empty must stay empty")
	require.Empty(t, cfg.Server.AsyncIngestQueueDir,
		"SERVER_ASYNC_INGEST_QUEUE_DIR empty must stay empty")
}

// TestApplyEnvOverrides_DefaultsSurviveResolvePath pins that
// the cwd-relative defaults DefaultConfig() installs for
// paths.data_dir and vectordb.persistence_dir are NOT
// touched by the tilde-expansion step. DefaultConfig
// resolves `wd` once at startup; the field already holds a
// non-tilde path; resolvePath must leave it alone.
//
// If a future refactor accidentally runs resolvePath on the
// wd-derived path, this test will catch the regression.
func TestApplyEnvOverrides_DefaultsSurviveResolvePath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DATA_DIR", "")
	t.Setenv("VECTOR_DB_DIR", "")

	before := DefaultConfig()
	dataDirBefore := before.Paths.DataDir
	vecDirBefore := before.VectorDB.PersistenceDir
	require.NotEmpty(t, dataDirBefore)
	require.NotEmpty(t, vecDirBefore)
	require.NotContains(t, dataDirBefore, "~",
		"DefaultConfig must not produce a tilde-bearing data_dir")
	require.NotContains(t, vecDirBefore, "~",
		"DefaultConfig must not produce a tilde-bearing persistence_dir")

	before.ApplyEnvOverrides()

	require.Equal(t, dataDirBefore, before.Paths.DataDir,
		"DefaultConfig's data_dir must survive ApplyEnvOverrides untouched")
	require.Equal(t, vecDirBefore, before.VectorDB.PersistenceDir,
		"DefaultConfig's persistence_dir must survive ApplyEnvOverrides untouched")
}

// TestLoadConfig_DataDirTildeInYAML pins that a leading "~/"
// in paths.data_dir in the YAML file is expanded to the
// user's home directory. This is the YAML-value counterpart
// of TestApplyEnvOverrides_PathFieldsTildeExpanded; both go
// through the same post-processing step so the YAML form is
// the secondary confirmation that the contract holds.
func TestLoadConfig_DataDirTildeInYAML(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	cfg := DefaultConfig()
	cfg.Server.Port = 9101
	// Overwrite the default data_dir with a tilde value.
	cfg.Paths.DataDir = "~/ragabast-yaml-data"
	require.NoError(t, cfg.SaveConfig(filepath.Join(tmp, "config.yml")))

	// Make sure env doesn't override what we just wrote.
	t.Setenv("DATA_DIR", "")

	loaded, err := LoadConfig(filepath.Join(tmp, "config.yml"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tmp, "ragabast-yaml-data"), loaded.Paths.DataDir,
		"paths.data_dir: ~/... in YAML must resolve to HOME/...")
}

// TestLoadConfig_VectorDBDirTildeInYAML pins the same contract
// for vectordb.persistence_dir — the other high-value path
// field operators configure by hand.
func TestLoadConfig_VectorDBDirTildeInYAML(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	cfg := DefaultConfig()
	cfg.Server.Port = 9102
	cfg.VectorDB.PersistenceDir = "~/vectors-yaml"
	require.NoError(t, cfg.SaveConfig(filepath.Join(tmp, "config.yml")))
	t.Setenv("VECTOR_DB_DIR", "")

	loaded, err := LoadConfig(filepath.Join(tmp, "config.yml"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(tmp, "vectors-yaml"), loaded.VectorDB.PersistenceDir,
		"vectordb.persistence_dir: ~/... in YAML must resolve to HOME/...")
}
