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

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestOpenAIEmbeddingClient_GenerateEmbedding_PostsToV1Embeddings(t *testing.T) {
	t.Parallel()

	var got struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],"model":"nomic-embed-text:v1.5"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "nomic-embed-text:v1.5", "", 5*time.Second)

	vec, err := client.GenerateEmbedding(context.Background(), "hello world")
	require.NoError(t, err)
	require.Equal(t, []float32{0.1, 0.2, 0.3}, vec)
	require.Equal(t, "nomic-embed-text:v1.5", got.Model)
	require.Equal(t, []string{"hello world"}, got.Input)
	require.Empty(t, gotAuth, "no auth header should be set when APIKey is empty")
}

func TestOpenAIEmbeddingClient_GenerateEmbedding_SendsAuthHeader(t *testing.T) {
	t.Parallel()

	gotAuthCh := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthCh <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "secret-key", 5*time.Second)
	_, err := client.GenerateEmbedding(context.Background(), "hi")
	require.NoError(t, err)

	select {
	case got := <-gotAuthCh:
		require.Equal(t, "Bearer secret-key", got)
	default:
		t.Fatal("did not observe Authorization header")
	}
}

func TestOpenAIEmbeddingClient_GenerateEmbeddingsBatch_SendsAllInputs(t *testing.T) {
	t.Parallel()

	var got struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		// Echo back one vector per input.
		data := make([]map[string]any, len(got.Input))
		for i := range got.Input {
			data[i] = map[string]any{
				"object":    "embedding",
				"index":     i,
				"embedding": []float32{float32(i), float32(i + 1)},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   data,
		})
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	vecs, err := client.GenerateEmbeddingsBatch(context.Background(), []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, vecs, 3)
	require.Equal(t, []string{"a", "b", "c"}, got.Input)
	require.Equal(t, []float32{0, 1}, vecs[0])
	require.Equal(t, []float32{2, 3}, vecs[2])
}

func TestOpenAIEmbeddingClient_EmptyTextReturnsError(t *testing.T) {
	t.Parallel()

	client := NewOpenAIEmbeddingClientWithOptions("http://example.invalid", "m", "", time.Second)
	_, err := client.GenerateEmbedding(context.Background(), "")
	require.ErrorIs(t, err, models.ErrEmbeddingFailed)
}

func TestOpenAIEmbeddingClient_EmptyBatchReturnsError(t *testing.T) {
	t.Parallel()

	client := NewOpenAIEmbeddingClientWithOptions("http://example.invalid", "m", "", time.Second)
	_, err := client.GenerateEmbeddingsBatch(context.Background(), nil)
	require.ErrorIs(t, err, models.ErrEmbeddingFailed)
}

func TestOpenAIEmbeddingClient_ServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	_, err := client.GenerateEmbedding(context.Background(), "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}

func TestOpenAIEmbeddingClient_ApiErrorInBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"context length exceeded","type":"invalid_request_error","code":"context_length_exceeded"},"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	_, err := client.GenerateEmbedding(context.Background(), "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "context length exceeded")
}

func TestOpenAIEmbeddingClient_MismatchedInputCountReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Return only 1 vector for a 3-text batch.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	_, err := client.GenerateEmbeddingsBatch(context.Background(), []string{"a", "b", "c"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "1 vectors for 3 inputs")
}

func TestOpenAIEmbeddingClient_EmptyVectorReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	_, err := client.GenerateEmbedding(context.Background(), "x")
	require.ErrorIs(t, err, models.ErrEmbeddingFailed)
}

func TestOpenAIEmbeddingClient_GenerateChunkEmbedding_UsesFullPath(t *testing.T) {
	t.Parallel()

	var gotInput string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			Input []string `json:"input"`
		}
		_ = json.Unmarshal(body, &parsed)
		if len(parsed.Input) > 0 {
			gotInput = parsed.Input[0]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	chunk := models.NewChunk()
	chunk.Content = "body"
	chunk.HeaderPath = "H1/H2"
	_, err := client.GenerateChunkEmbedding(context.Background(), chunk)
	require.NoError(t, err)
	require.Contains(t, gotInput, "H1/H2")
	require.Contains(t, gotInput, "body")
}

func TestOpenAIEmbeddingClient_ValidateConnection_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("expected /v1/models, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	require.NoError(t, client.ValidateConnection(context.Background()))
}

func TestOpenAIEmbeddingClient_ValidateConnection_FallsBackToUnversionedPath(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var paths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		switch r.URL.Path {
		case "/v1/models":
			w.WriteHeader(http.StatusNotFound)
		case "/models":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	require.NoError(t, client.ValidateConnection(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, paths, "/v1/models")
	require.Contains(t, paths, "/models")
}

func TestOpenAIEmbeddingClient_ValidateConnection_BothPathsFail(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	err := client.ValidateConnection(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not respond")
}

// Helper: confirm that the client doesn't accidentally double-wrap inputs.
func TestOpenAIEmbeddingClient_SingleInputBatch_SendsArrayOfOne(t *testing.T) {
	t.Parallel()

	var got struct {
		Input []string `json:"input"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "m", "", 5*time.Second)
	_, err := client.GenerateEmbedding(context.Background(), "only one")
	require.NoError(t, err)
	require.Len(t, got.Input, 1)
	require.Equal(t, "only one", got.Input[0])
}

func TestOpenAIEmbeddingClient_DefaultsAreLocal(t *testing.T) {
	t.Parallel()

	// No API key, no base URL, no model -> defaults are local-friendly.
	client := NewOpenAIEmbeddingClientWithOptions("", "", "", 0)
	require.Equal(t, "http://localhost:11434", client.baseURLString())
	require.NotEmpty(t, client.modelName())
}
