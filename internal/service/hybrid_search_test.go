package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ------------------------------------------------------------------
// helpers
// ------------------------------------------------------------------

// hybridSearchStubEmbeddingsServer mimics Ollama's
// /v1/embeddings endpoint. Every input gets a deterministic
// unit vector so the cosine similarity ranking is reproducible
// across runs.
func hybridSearchStubEmbeddingsServer(t *testing.T, dim int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.Error(w, "stub: unknown path", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "stub: read", http.StatusBadRequest)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "stub: parse", http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, 0, len(req.Input))
		for i := range req.Input {
			vec := make([]float32, dim)
			vec[i%dim] = 1.0
			data = append(data, map[string]any{
				"object": "embedding", "index": i, "embedding": vec,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "data": data, "model": "stub-embed",
		})
	}))
}

// newHybridTestService builds a real Service wired to an
// in-memory vector DB + a bleve search index in a temp dir,
// backed by a stub embeddings HTTP server. Lets the tests
// exercise Service.HybridSearch end-to-end without needing
// a real Ollama installation.
func newHybridTestService(t *testing.T) *Service {
	t.Helper()

	const dim = 4
	srv := hybridSearchStubEmbeddingsServer(t, dim)
	t.Cleanup(srv.Close)

	cfg := config.DefaultConfig()
	cfg.Ollama.BaseURL = srv.URL
	cfg.Ollama.ChatBaseURL = srv.URL
	cfg.VectorDB.PersistenceDir = t.TempDir()
	cfg.VectorDB.EmbeddingDimension = dim

	svc, err := NewService(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		// No explicit Close on Service yet; the in-memory
		// vector DB and bleve index are GC'd with the temp
		// dir. Bleve flushes on every batch.
	})
	return svc
}

// mustHybridTestDoc renders a docbuilder Markdown source
// with the requested UID, title, and body. The fingerprint
// is derived from the doc ID (the chunker uses the
// fingerprint to detect re-ingests; for the test it's
// enough that it's stable per doc).
func mustHybridTestDoc(t *testing.T, id, body string) string {
	t.Helper()
	// Frontmatter that passes models.Document.Validate().
	// Title is mandatory for the parser; UID is required
	// by the service's chunking pipeline.
	src := strings.Join([]string{
		"---",
		"uid: " + id,
		"fingerprint: fp-" + id,
		"title: Doc " + id,
		"tags: [t1]",
		"---",
		"",
		body,
	}, "\n")
	return src
}

// vectorMode parses a mode name string into the vector
// package's SearchMode enum. The enum is unexported by
// name so a string-keyed helper keeps the test in sync.
// t.Fatal is reached on unknown input; we panic for the
// linter since the test framework never returns.
func vectorMode(t *testing.T, name string) vector.SearchMode {
	t.Helper()
	switch name {
	case "hybrid":
		return vector.ModeHybrid
	case "semantic":
		return vector.ModeSemantic
	case "keyword":
		return vector.ModeKeyword
	default:
		t.Fatalf("vectorMode: unknown mode %q", name)
		panic("unreachable")
	}
}

// TestService_HybridSearch exercises the mode knob and the
// filter translation end-to-end. We use the same fixture as
// the operations integration tests: in-memory vector DB +
// bleve keyword index, both wired through VectorOperations.
func TestService_HybridSearch(t *testing.T) {
	t.Parallel()

	svc := newHybridTestService(t)

	_, err := svc.IngestDocument(
		context.Background(),
		mustHybridTestDoc(t, "doc-a", "kubernetes ingress tls"))
	require.NoError(t, err)
	_, err = svc.IngestDocument(
		context.Background(),
		mustHybridTestDoc(t, "doc-b", "kubernetes networking policy"))
	require.NoError(t, err)
	_, err = svc.IngestDocument(
		context.Background(),
		mustHybridTestDoc(t, "doc-c", "filler unrelated"))
	require.NoError(t, err)

	t.Run("hybrid mode returns both-store matches", func(t *testing.T) {
		results, err := svc.HybridSearch(context.Background(),
			"kubernetes", 5, SearchFilters{}, vectorMode(t, "hybrid"))
		require.NoError(t, err)
		require.NotEmpty(t, results)
		// Chunk IDs are content hashes from the chunker, so
		// assert by document_id (which the chunker copies
		// from the parent document).
		docIDs := searchDocIDs(results)
		assert.Contains(t, docIDs, "doc-a")
		assert.Contains(t, docIDs, "doc-b")
	})

	t.Run("semantic mode falls back to embedding-only", func(t *testing.T) {
		results, err := svc.HybridSearch(context.Background(),
			"kubernetes", 5, SearchFilters{}, vectorMode(t, "semantic"))
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.LessOrEqual(t, len(results), 5)
	})

	t.Run("keyword mode returns keyword-side matches", func(t *testing.T) {
		results, err := svc.HybridSearch(context.Background(),
			"kubernetes", 5, SearchFilters{}, vectorMode(t, "keyword"))
		require.NoError(t, err)
		require.NotEmpty(t, results)
		docIDs := searchDocIDs(results)
		assert.Contains(t, docIDs, "doc-a")
		assert.Contains(t, docIDs, "doc-b")
	})

	t.Run("filter is pre-applied to both rankings", func(t *testing.T) {
		results, err := svc.HybridSearch(context.Background(),
			"kubernetes", 5,
			SearchFilters{DocumentID: "doc-a"},
			vectorMode(t, "hybrid"))
		require.NoError(t, err)
		require.NotEmpty(t, results)
		for _, r := range results {
			assert.Equal(t, "doc-a", r.DocumentID,
				"document_id filter must apply in hybrid mode")
		}
	})

	t.Run("invalid mode returns error", func(t *testing.T) {
		_, err := svc.HybridSearch(context.Background(),
			"kubernetes", 5, SearchFilters{}, vector.SearchMode(99))
		assert.Error(t, err)
	})
}

// TestService_Search_PreservesV030Behavior pins the v0.3.0
// semantic-only contract: callers that depend on it (the
// search-tests, the chat handler) see identical results.
func TestService_Search_PreservesV030Behavior(t *testing.T) {
	t.Parallel()

	svc := newHybridTestService(t)

	_, err := svc.IngestDocument(
		context.Background(),
		mustHybridTestDoc(t, "doc-a", "kubernetes ingress tls"))
	require.NoError(t, err)

	// Service.Search is the v0.3.0 path; pure semantic.
	// In a stubbed embeddings world the result depends on
	// the embedding values, so we only assert non-empty.
	results, err := svc.Search(context.Background(), "kubernetes", 5, SearchFilters{})
	require.NoError(t, err)
	assert.NotEmpty(t, results, "Service.Search must still work")
}

// ------------------------------------------------------------------
// helpers
// ------------------------------------------------------------------

// searchDocIDs extracts the document IDs from a result list.
// Chunk IDs are content hashes produced by the chunker; tests
// that care about which DOCUMENT a hit belongs to should use
// this helper rather than asserting on ChunkID.
func searchDocIDs(results []models.SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.DocumentID)
	}
	return ids
}
