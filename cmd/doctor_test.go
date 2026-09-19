package cmd

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureLogs redirects the global log package's output to a
// buffer for the duration of fn, then restores the original
// writer. Used by the doctor tests so we can assert on what the
// command actually prints without coupling to fmt vs log vs
// stdout.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	buf := &bytes.Buffer{}
	oldOut := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	})
	fn()
	return buf.String()
}

// TestDoctor_ConfigValidAndDimensionsMatch pins the happy path:
// model is in the known-dim table, configured dim matches.
// Should print success and exit 0.
func TestDoctor_ConfigValidAndDimensionsMatch(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfg := doctorConfig(
		"nomic-embed-text:v1.5",
		768,
		filepath.Join(tmp, "data", "vectors"),
		filepath.Join(tmp, "data"),
	)
	cfgPath := writeDoctorConfig(t, cfg)

	out := captureLogs(t, func() {
		cmd := DoctorCmd{ConfigOpts: ConfigOpts{Config: cfgPath}}
		require.NoError(t, cmd.Run(nil))
	})

	require.Contains(t, out, "nomic-embed-text",
		"doctor must name the embedding model so the user sees what was checked")
	require.Contains(t, out, "768")
}

// TestDoctor_DimensionMismatchWarns pins the case that bit the
// user: embedding_dimensions is 768 (from a previous config)
// but the configured model is gemini-embedding-2 (3072-dim).
// Doctor must call this out, not silently pass.
func TestDoctor_DimensionMismatchWarns(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfg := doctorConfig(
		"gemini-embedding-2",
		768, // WRONG: gemini-embedding-2 is 3072-dim
		filepath.Join(tmp, "data", "vectors"),
		filepath.Join(tmp, "data"),
	)
	cfgPath := writeDoctorConfig(t, cfg)

	out := captureLogs(t, func() {
		cmd := DoctorCmd{ConfigOpts: ConfigOpts{Config: cfgPath}}
		// Dimension mismatch is a WARNING, not a hard error.
		// Run must succeed so doctor can be used in CI / cron.
		require.NoError(t, cmd.Run(nil),
			"dimension mismatch is informational, not a hard error")
	})

	require.Contains(t, out, "gemini-embedding-2")
	require.Contains(t, out, "3072",
		"doctor must mention the actual model dimension so the user sees what to set")
	require.Contains(t, out, "768",
		"doctor must mention the configured dimension")
	require.True(t,
		strings.Contains(out, "WARN") || strings.Contains(out, "⚠"),
		"the mismatch line must be visually flagged as a warning; got: %s", out)
}

// TestDoctor_UnknownModelIsAcknowledged pins the case where the
// user is running a model not in the known-dim table (custom
// finetune, a new provider's model, etc.). Doctor must NOT
// pretend to know the dimension — it must say so explicitly so
// the user doesn't falsely conclude the match is verified.
func TestDoctor_UnknownModelIsAcknowledged(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfg := doctorConfig(
		"my-custom-finetune-v3",
		768,
		filepath.Join(tmp, "data", "vectors"),
		filepath.Join(tmp, "data"),
	)
	cfgPath := writeDoctorConfig(t, cfg)

	out := captureLogs(t, func() {
		cmd := DoctorCmd{ConfigOpts: ConfigOpts{Config: cfgPath}}
		require.NoError(t, cmd.Run(nil))
	})

	require.Contains(t, out, "my-custom-finetune-v3")
	require.True(t,
		strings.Contains(out, "unknown") || strings.Contains(out, "Unknown") || strings.Contains(out, "not in the known"),
		"unknown model must be flagged as such; got: %s", out)
}

// TestDoctor_ConfigValidationFailsIsHardError pins that a
// structurally-invalid config (e.g. invalid OllamaOptions) IS a
// hard error: doctor exits non-zero. This is distinct from the
// dimension mismatch (which is a warning).
func TestDoctor_ConfigValidationFailsIsHardError(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	// Force top_p out of range [0,1] so cfg.Validate() fails.
	persistDir := filepath.Join(tmp, "data", "vectors")
	dataDir := filepath.Join(tmp, "data")
	cfg := `ollama:
  base_url: http://127.0.0.1:8000/v1
  embedding_model: x
  chat_base_url: http://127.0.0.1:8000/v1
  chat_model: x
  timeout: 30s
  options:
    top_p: 2.0
vectordb:
  persistence_dir: ` + persistDir + `
  collection_name: test
  embedding_dimension: 768
server:
  address: 0.0.0.0
  port: 8080
processing:
  max_chunk_size: 2000
  min_chunk_size: 300
  chunk_overlap: 150
paths:
  data_dir: ` + dataDir + `
  templates_dir: ""
`
	cfgPath := writeDoctorConfig(t, cfg)

	captureLogs(t, func() {
		cmd := DoctorCmd{ConfigOpts: ConfigOpts{Config: cfgPath}}
		require.Error(t, cmd.Run(nil),
			"a config that fails cfg.Validate() must make doctor return an error")
	})
}

// doctorConfig builds a minimal valid YAML config string with the
// given embedding model name, dimension, and persistence paths.
func doctorConfig(embeddingModel string, embeddingDim int, persistenceDir, dataDir string) string {
	return `ollama:
  base_url: http://127.0.0.1:8000/v1
  embedding_model: ` + embeddingModel + `
  chat_base_url: http://127.0.0.1:8000/v1
  chat_model: x
  timeout: 30s
vectordb:
  persistence_dir: ` + persistenceDir + `
  collection_name: test
  embedding_dimension: ` + strconv.Itoa(embeddingDim) + `
server:
  address: 0.0.0.0
  port: 8080
processing:
  max_chunk_size: 2000
  min_chunk_size: 300
  chunk_overlap: 150
paths:
  data_dir: ` + dataDir + `
  templates_dir: ""
`
}

// writeDoctorConfig writes a YAML config string to disk and
// returns the file path.
func writeDoctorConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}
