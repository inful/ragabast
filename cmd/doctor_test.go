package cmd

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
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

// TestDoctor_StoreStateMismatchWarns pins the case doctor was
// missing before: config says embedding_dimension 768 but the
// store already contains vectors of length 3072 (stale data
// from a previous model, or a partial reset, or an upgrade
// from a pre-7c9d6d8 ragabast that didn't fail-fast at ingest).
// This is the case where `ragabast doctor` should be the
// authoritative answer even when the config-level model/dim
// table check passes — config and store can disagree.
//
// The warning text depends on which chromem-go error path the
// store falls into. A uniform-length-but-wrong-dim store
// surfaces the actual stored dimension; a mixed-dim store
// (the realistic post-upgrade scenario) trips chromem-go at
// the similarity step before we can read the length. Either
// path must produce a warning with the recovery command.
func TestDoctor_StoreStateMismatchWarns(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	persistDir := filepath.Join(tmp, "data", "vectors")
	dataDir := filepath.Join(tmp, "data")

	// Seed the store with 3072-dim chunks to simulate stale
	// data from a previous model. We bypass the
	// checkEmbeddingDimension guard by constructing a VectorDB
	// whose configured dim matches what we're about to write.
	seedDB, err := newSeedVectorDB(persistDir, 3072)
	require.NoError(t, err)
	seedChunk := &chunkForSeed{ID: "c1", DocumentID: "d1"}
	seedVec := make([]float32, 3072)
	require.NoError(t, seedDB.addChunkForSeed(seedChunk, seedVec))

	// Config says 768. Doctor must catch the mismatch.
	cfg := doctorConfig("nomic-embed-text:v1.5", persistDir, dataDir)
	cfgPath := writeDoctorConfig(t, cfg)

	out := captureLogs(t, func() {
		cmd := DoctorCmd{ConfigOpts: ConfigOpts{Config: cfgPath}}
		require.NoError(t, cmd.Run(nil),
			"store-state mismatch is a warning, not a hard error")
	})

	require.Contains(t, out, "vector reset --force",
		"doctor must point at the recovery command")
	// Either of these two strings indicates the mismatch was
	// detected. The exact wording depends on whether the store
	// is uniform-length-but-wrong (3072 surfaces cleanly) or
	// mixed-dim (chromem-go bails out at the similarity step
	// and we report that error). Both are valid detections.
	detectedAsMismatch := strings.Contains(out, "3072") ||
		strings.Contains(out, "vectors must have the same length")
	require.True(t, detectedAsMismatch,
		"doctor must surface either the actual stored dim (3072) or the chromem-go corruption signal; got: %s", out)
}

// TestDoctor_StoreStateMatchOk pins the happy path: when the
// store actually contains 768-dim vectors and the config says
// 768, the new store-state check passes.
func TestDoctor_StoreStateMatchOk(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	persistDir := filepath.Join(tmp, "data", "vectors")
	dataDir := filepath.Join(tmp, "data")

	// Seed with 768-dim chunks.
	seedDB, err := newSeedVectorDB(persistDir, 768)
	require.NoError(t, err)
	seedVec := make([]float32, 768)
	require.NoError(t, seedDB.addChunkForSeed(&chunkForSeed{ID: "c1", DocumentID: "d1"}, seedVec))

	cfg := doctorConfig("nomic-embed-text:v1.5", persistDir, dataDir)
	cfgPath := writeDoctorConfig(t, cfg)

	out := captureLogs(t, func() {
		cmd := DoctorCmd{ConfigOpts: ConfigOpts{Config: cfgPath}}
		require.NoError(t, cmd.Run(nil))
	})

	require.Contains(t, out, "stored embedding dimension (768) matches",
		"doctor must explicitly affirm the store-state match; got: %s", out)
}

// TestDoctor_EmptyStorePasses pins that an empty (or missing)
// store does not produce a warning — there's nothing to verify
// against, and the next ingest will fill the store at the
// configured dim (which the prior model/dim table check
// already covers).
func TestDoctor_EmptyStorePasses(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	persistDir := filepath.Join(tmp, "data", "vectors") // not created
	dataDir := filepath.Join(tmp, "data")

	cfg := doctorConfig("nomic-embed-text:v1.5", persistDir, dataDir)
	cfgPath := writeDoctorConfig(t, cfg)

	out := captureLogs(t, func() {
		cmd := DoctorCmd{ConfigOpts: ConfigOpts{Config: cfgPath}}
		require.NoError(t, cmd.Run(nil))
	})

	require.Contains(t, out, "empty",
		"doctor must explicitly note the empty-store state; got: %s", out)
}

// TestDoctor_CheckServerDetectsActualDimMismatch pins the
// failure mode that bit the user: the config says 768 and the
// configured model is in the known-dim table as 768, but the
// embedding server actually returns 3072-dim vectors for the
// configured model. The config-level model/dim table check
// cannot catch this — only a real server probe can.
//
// The test stands up an httptest server that mimics the
// OpenAI-compat /v1/embeddings endpoint but returns 3072 floats
// per vector regardless of the dimensions request. With
// --check-server, doctor must call the server, observe the
// 3072-dim response, and warn that the actual dim differs from
// the configured 768.
func TestDoctor_CheckServerDetectsActualDimMismatch(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	// Stand up an embeddings server that always returns
	// 3072 floats. base_url will be its URL. We handle /v1/models
	// too so ValidateConnection's reachability probe passes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models", "/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/embeddings":
			vec := make([]float32, 3072)
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"object":"list","data":[{"object":"embedding","index":0,"embedding":%s}]}`,
				encodeF32Slice(vec),
			)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	persistDir := filepath.Join(tmp, "data", "vectors")
	dataDir := filepath.Join(tmp, "data")
	cfg := doctorConfigServer(srv.URL, "nomic-embed-text:v1.5", 768, persistDir, dataDir)
	cfgPath := writeDoctorConfig(t, cfg)

	out := captureLogs(t, func() {
		cmd := DoctorCmd{
			ConfigOpts:  ConfigOpts{Config: cfgPath},
			CheckServer: true,
		}
		require.NoError(t, cmd.Run(nil),
			"a server-side dim mismatch is a warning, not a hard error")
	})

	require.Contains(t, out, "3072",
		"doctor must mention the actual dim the server returned")
	require.Contains(t, out, "768",
		"doctor must mention the configured dim")
	require.Contains(t, out, "vectordb.embedding_dimension",
		"doctor output must name the configured field by name so the user can find it")
}

// TestDoctor_CheckServerDimMatches pins the happy path: when
// the server returns the dim the config says, the
// server-probe check passes.
func TestDoctor_CheckServerDimMatches(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models", "/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/embeddings":
			vec := make([]float32, 768)
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"object":"list","data":[{"object":"embedding","index":0,"embedding":%s}]}`,
				encodeF32Slice(vec),
			)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	persistDir := filepath.Join(tmp, "data", "vectors")
	dataDir := filepath.Join(tmp, "data")
	cfg := doctorConfigServer(srv.URL, "nomic-embed-text:v1.5", 768, persistDir, dataDir)
	cfgPath := writeDoctorConfig(t, cfg)

	out := captureLogs(t, func() {
		cmd := DoctorCmd{
			ConfigOpts:  ConfigOpts{Config: cfgPath},
			CheckServer: true,
		}
		require.NoError(t, cmd.Run(nil))
	})

	require.Contains(t, out, "matches the configured",
		"doctor must affirm the server-probe dim match; got: %s", out)
}

// doctorConfigServer is like doctorConfig but takes an explicit
// embeddings base URL (so tests can point at httptest servers)
// and an explicit embedding_dim (so tests can express the
// "config says 768 but server returns 3072" mismatch).
func doctorConfigServer(baseURL, embeddingModel string, embeddingDim int, persistenceDir, dataDir string) string {
	return fmt.Sprintf(`ollama:
  base_url: %s
  embedding_model: %s
  chat_base_url: http://127.0.0.1:8000/v1
  chat_model: x
  timeout: 30s
vectordb:
  persistence_dir: %s
  collection_name: test
  embedding_dimension: %d
server:
  address: 0.0.0.0
  port: 8080
processing:
  max_chunk_size: 2000
  min_chunk_size: 300
  chunk_overlap: 150
paths:
  data_dir: %s
  templates_dir: ""
`, baseURL, embeddingModel, persistenceDir, embeddingDim, dataDir)
}

// encodeF32Slice formats a []float32 as a JSON array literal
// (e.g. "[0.5,0.25,0]") suitable for embedding in a fake
// /v1/embeddings response body.
func encodeF32Slice(v []float32) string {
	var buf strings.Builder
	buf.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(strconv.FormatFloat(float64(x), 'f', -1, 32))
	}
	buf.WriteByte(']')
	return buf.String()
}

// doctorConfig builds a minimal valid YAML config string with the
// given embedding model name, dimension, and persistence paths.
// The embedding dimension is hardcoded to 768 — every existing
// doctor test calls doctorConfig with 768 because that's the
// "expected" configured dim, and any test that needs a
// different dim goes through writeDoctorConfig directly. Keeping
// the parameter would just be dead flexibility; the unparam
// linter agrees.
func doctorConfig(embeddingModel, persistenceDir, dataDir string) string {
	return `ollama:
  base_url: http://127.0.0.1:8000/v1
  embedding_model: ` + embeddingModel + `
  chat_base_url: http://127.0.0.1:8000/v1
  chat_model: x
  timeout: 30s
vectordb:
  persistence_dir: ` + persistenceDir + `
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
}

// writeDoctorConfig writes a YAML config string to disk and
// returns the file path.
func writeDoctorConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// chunkForSeed is a small struct that satisfies the parts of
// *models.Chunk the vector DB needs (ID, DocumentID, etc.) for
// seeding without coupling the doctor tests to the full models
// package surface.
type chunkForSeed struct {
	ID         string
	DocumentID string
}

// newSeedVectorDB opens (or creates) a VectorDB at the given
// path with the given embedding dimension and seeds one chunk.
// It is used by the doctor tests to construct the "store
// already has data" scenario without going through the full
// service layer.
func newSeedVectorDB(persistDir string, dim int) (*seedVectorDB, error) {
	return newSeedVectorDBAt(persistDir, dim)
}

// addChunkForSeed injects a chunk with the given embedding into
// the seed store. The seed chunk is a minimal models.Chunk
// shape; the doc-level metadata fields the seed-store uses are
// populated with safe defaults.
func (db *seedVectorDB) addChunkForSeed(chunk *chunkForSeed, embedding []float32) error {
	full := &modelsChunkForSeed{
		ID:                  chunk.ID,
		DocumentID:          chunk.DocumentID,
		DocumentTitle:       "T",
		HeaderPath:          "",
		Level:               1,
		StartLine:           1,
		EndLine:             1,
		Content:             "seed",
		DocumentFingerprint: "seed",
		UID:                 chunk.DocumentID,
	}
	return db.addChunk(full, embedding)
}
