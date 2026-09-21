package vector

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
)

// stubEmbeddingsServer returns a httptest.Server that mimics
// Ollama's /v1/embeddings endpoint. Every input text gets the
// same fixed-dimension vector back, where dimension is the
// dim argument. This keeps integration tests independent of
// any real embeddings server.
func stubEmbeddingsServer(t *testing.T, dim int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.Error(w, "stub: unknown path", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "stub: read body", http.StatusBadRequest)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "stub: parse body", http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, 0, len(req.Input))
		for i := range req.Input {
			vec := make([]float32, dim)
			// Deterministic per-index vector — first
			// element is 1.0, rest are 0.0. Lets us spot
			// cross-chunk contamination in tests.
			vec[i%dim] = 1.0
			data = append(data, map[string]any{
				"object":    "embedding",
				"index":     i,
				"embedding": vec,
			})
		}
		out := map[string]any{
			"object": "list",
			"data":   data,
			"model":  "stub-embed",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
}

// stubEmbeddings returns a real OpenAIEmbeddingClient pointed
// at the stub server. The dimension is fixed at 4 (the same
// dim used by fixtureVO to create the VectorDB under test).
// Single value is enough for the integration tests in this
// package; if a future test needs a different dim, change
// this constant or extend the helper to take one.
func stubEmbeddings(t *testing.T) *OpenAIEmbeddingClient {
	t.Helper()
	const dim = 4
	srv := stubEmbeddingsServer(t, dim)
	t.Cleanup(srv.Close)
	return NewOpenAIEmbeddingClientWithOptions(srv.URL, "stub-embed", "", 5*time.Second, dim, 1)
}

// testDoc builds a *models.Document with a single chunk whose
// content is chunkContent. doc.Content (the parent-document
// content) is set to docContent to satisfy Validate(). Both
// chunk IDs and the parent fingerprint are populated so both
// stores can index the document.
func testDoc(t *testing.T, id, docContent, chunkContent string) *models.Document {
	t.Helper()
	doc := &models.Document{
		ID:          id,
		Title:       "Doc " + id,
		UID:         id + "-uid",
		Fingerprint: id + "-fp",
		Content:     docContent,
		Tags:        []string{"t1"},
		Categories:  []string{"c1"},
		Chunks: []models.Chunk{
			{
				ID:                 id + "-c0",
				DocumentID:         id,
				Content:            chunkContent,
				DocumentTitle:      "Doc " + id,
				UID:                id + "-uid",
				Fingerprint:        id + "-fp",
				DocumentTags:       []string{"t1"},
				DocumentCategories: []string{"c1"},
			},
		},
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("testDoc: doc invalid: %v", err)
	}
	return doc
}

// testDocWithFingerprint is like testDoc but lets the caller
// set the fingerprint (for re-ingest tests that need a
// different fingerprint to trigger an update path).
func testDocWithFingerprint(t *testing.T, id, docContent, fp, chunkContent string) *models.Document {
	t.Helper()
	doc := testDoc(t, id, docContent, chunkContent)
	doc.Fingerprint = fp
	doc.Chunks[0].Fingerprint = fp
	return doc
}

// testDocTwoChunks builds a doc with two chunks. doc.Content
// is set to a concatenation of the two chunk contents so
// Validate() accepts the document.
func testDocTwoChunks(t *testing.T, id, first, second string) *models.Document {
	t.Helper()
	doc := &models.Document{
		ID:          id,
		Title:       "Doc " + id,
		UID:         id + "-uid",
		Fingerprint: id + "-fp",
		Content:     first + "\n\n" + second,
		Tags:        []string{"t1"},
		Categories:  []string{"c1"},
		Chunks: []models.Chunk{
			{
				ID:                 id + "-c0",
				DocumentID:         id,
				Content:            first,
				DocumentTitle:      "Doc " + id,
				UID:                id + "-uid",
				Fingerprint:        id + "-fp",
				DocumentTags:       []string{"t1"},
				DocumentCategories: []string{"c1"},
			},
			{
				ID:                 id + "-c1",
				DocumentID:         id,
				Content:            second,
				DocumentTitle:      "Doc " + id,
				UID:                id + "-uid",
				Fingerprint:        id + "-fp",
				DocumentTags:       []string{"t1"},
				DocumentCategories: []string{"c1"},
			},
		},
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("testDocTwoChunks: doc invalid: %v", err)
	}
	return doc
}

// compile-time assertion: Ensure context import is present so
// goimports doesn't strip it (test helpers below use ctx).
var (
	_ = fmt.Sprintf
	_ = strings.TrimSpace
)
