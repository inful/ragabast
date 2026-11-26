package vector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenAIEmbeddingClient_NotFoundNamesTheURL pins the
// diagnostics contract for the 404 case: the error must name the
// URL that was called. A 404 from an embeddings server means the
// path is wrong, and the operator cannot tell which path was tried
// unless the error says so.
func TestOpenAIEmbeddingClient_NotFoundNamesTheURL(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "embeddinggemma-2", "", 5*time.Second, 0, 1, "", "")

	_, err := client.GenerateEmbedding(context.Background(), "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), srv.URL+"/v1/embeddings",
		"the error must name the URL that was called")
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "embedding_gemma",
		"a 404 must point at the other provider — that is the usual cause")
}

// TestEmbeddingGemmaClient_NotFoundNamesTheURL pins the same
// contract on the /v2/embed client.
func TestEmbeddingGemmaClient_NotFoundNamesTheURL(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	_, err := client.GenerateEmbedding(context.Background(), "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), srv.URL+"/v2/embed",
		"the error must name the URL that was called")
	assert.Contains(t, err.Error(), "ollama.base_url",
		"a 404 must point at the OpenAI-compatible path — that is the usual cause")
}

// TestEmbeddingGemmaClient_ValidateConnectionNamesTheURL pins the
// same for the doctor probe: an operator running
// `ragabast doctor --check-server` must see which URL was probed.
func TestEmbeddingGemmaClient_ValidateConnectionNamesTheURL(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	err := client.ValidateConnection(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), srv.URL+"/v2/embed",
		"the probe error must name the URL that was probed")
}

// TestOpenAIEmbeddingClient_ErrorResponseKeepsTheBody pins that
// the URL addition did not cost the server's own error text — the
// operator still sees what the server said.
func TestOpenAIEmbeddingClient_ErrorResponseKeepsTheBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"model not loaded"}}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second, 0, 1, "", "")

	_, err := client.GenerateEmbedding(context.Background(), "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model not loaded",
		"the server's own message must survive")
	assert.Contains(t, err.Error(), srv.URL,
		"the URL must be in the message too")
}

// TestOpenAIEmbeddingClient_SuccessUnaffected pins that the happy
// path is untouched by the diagnostics change.
func TestOpenAIEmbeddingClient_SuccessUnaffected(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		out, _ := json.Marshal(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": []float64{0.1, 0.2}},
			},
		})
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second, 0, 1, "", "")

	vec, err := client.GenerateEmbedding(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, []float32{0.1, 0.2}, vec)
}
