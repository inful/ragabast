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

// captureEmbedInputs returns an httptest server that
// captures the input field from each /v1/embeddings
// body, normalizes it to []string regardless of whether
// the client sent an array (OpenAI spec) or a single
// string (Ollama spec), and returns the slice through
// the channel.
func captureEmbedInputs(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		switch v := got["input"].(type) {
		case []any:
			for _, in := range v {
				if s, ok := in.(string); ok {
					mu.Lock()
					seen = append(seen, s)
					mu.Unlock()
				}
			}
		case string:
			mu.Lock()
			seen = append(seen, v)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],"model":"m"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// TestVectorOperations_Search_PrependsQueryPrompt pins
// the operations-layer wiring: when the embedding
// client is configured with a query prompt, the
// /v1/embeddings body posted by Search carries that
// prompt prepended to the input. Without this, an
// operator switching to embedding-gemma would still
// embed the query as a free-form string — silently
// losing the model's retrieval-quality optimization.
//
// Uses a real in-memory VectorDB (via fixtureVO) so
// Search can run end-to-end without nil-pointer panics;
// the assertion only cares about the embedding request,
// not the search results.
func TestVectorOperations_Search_PrependsQueryPrompt(t *testing.T) {
	t.Parallel()

	srv, seen := captureEmbedInputs(t)

	embeddings := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "embeddinggemma", "", 5*time.Second, 0, 1,
		"title: none | text:",          // docPrompt
		"task: search result | query:", // queryPrompt
	)

	vo, _, _ := fixtureVO(t)
	// Swap the nil embeddings the fixture wired for
	// the one with prompts configured.
	vo.embeddings = embeddings

	_, _ = vo.Search(context.Background(), "what is ragabast", 5, nil)

	require.NotEmpty(t, *seen, "Search must POST to /v1/embeddings")
	require.Equal(t, "task: search result | query:what is ragabast", (*seen)[0],
		"Search must use the query prompt, not the doc prompt")
}

// TestVectorOperations_HybridSearch_PrependsQueryPrompt
// mirrors the Search test for the hybrid path: a query
// reaching /v1/embeddings from searchSemanticOnly must
// also carry the query prompt. Same threat model — both
// code paths embed the user's query.
func TestVectorOperations_HybridSearch_PrependsQueryPrompt(t *testing.T) {
	t.Parallel()

	srv, seen := captureEmbedInputs(t)

	embeddings := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "embeddinggemma", "", 5*time.Second, 0, 1,
		"title: none | text:",
		"task: search result | query:",
	)

	vo, _, _ := fixtureVO(t)
	vo.embeddings = embeddings

	_, _ = vo.SearchHybrid(context.Background(), "what is ragabast", 5, SearchFilters{}, ModeHybrid)

	require.NotEmpty(t, *seen, "SearchHybrid must POST to /v1/embeddings")
	require.Equal(t, "task: search result | query:what is ragabast", (*seen)[0],
		"SearchHybrid must use the query prompt")
}
