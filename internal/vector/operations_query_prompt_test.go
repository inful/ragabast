package vector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestVectorOperations_Search_PrependsQueryPrompt pins
// the operations-layer wiring: when the embedding
// client is configured with a query prompt, the
// /v1/embeddings body posted by Search carries that
// prompt prepended to the input. Without this, an
// operator switching to embedding-gemma would still
// embed the query as a free-form string — silently
// losing the model's retrieval-quality optimization.
//
// Test setup mirrors TestVectorOperations_DeleteDocument_*
// in operations_integration_test.go: a stub
// /v1/embeddings server records the body, the search
// side asserts on the recorded input. The VectorDB
// doesn't need real chromem-go state because Search
// fails fast on empty collection (and we only care
// about the embedding request, not the search result).
func TestVectorOperations_Search_PrependsQueryPrompt(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var seenInputs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if inputs, ok := got["input"].([]any); ok {
			for _, in := range inputs {
				if s, ok := in.(string); ok {
					mu.Lock()
					seenInputs = append(seenInputs, s)
					mu.Unlock()
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],"model":"m"}`))
	}))
	t.Cleanup(srv.Close)

	embeddings := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "embeddinggemma", "", 5*time.Second, 0, 1,
		"title: none | text:",          // docPrompt
		"task: search result | query:", // queryPrompt
	)

	// Search hits the embeddings server for the query
	// vector; the actual vector DB call returns empty
	// (no documents) but we only assert the embedding
	// request shape.
	vo := NewVectorOperations(nil, embeddings)
	_, _ = vo.Search(context.Background(), "what is ragabast", 5, nil)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, seenInputs, "Search must POST to /v1/embeddings")
	require.Equal(t, "task: search result | query:what is ragabast", seenInputs[0],
		"Search must use the query prompt, not the doc prompt")
}

// TestVectorOperations_HybridSearch_PrependsQueryPrompt
// mirrors the Search test for the hybrid path: a query
// reaching /v1/embeddings from searchSemanticOnly must
// also carry the query prompt. Same threat model — both
// code paths embed the user's query.
func TestVectorOperations_HybridSearch_PrependsQueryPrompt(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var seenInputs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if inputs, ok := got["input"].([]any); ok {
			for _, in := range inputs {
				if s, ok := in.(string); ok {
					mu.Lock()
					seenInputs = append(seenInputs, s)
					mu.Unlock()
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],"model":"m"}`))
	}))
	t.Cleanup(srv.Close)

	embeddings := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "embeddinggemma", "", 5*time.Second, 0, 1,
		"title: none | text:",
		"task: search result | query:",
	)

	vo := NewVectorOperations(nil, embeddings)
	// SearchHybrid without a keyword index falls into
	// searchSemanticOnly (same code path that calls the
	// embeddings client). We don't care about the
	// returned hits — only that the embedding request
	// carries the query prompt.
	_, _ = vo.SearchHybrid(context.Background(), "what is ragabast", 5, SearchFilters{}, SearchModeHybrid)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, seenInputs, "SearchHybrid must POST to /v1/embeddings")
	require.Equal(t, "task: search result | query:what is ragabast", seenInputs[0],
		"SearchHybrid must use the query prompt")
}
