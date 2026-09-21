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

// captureRequest returns an httptest.Server whose handler
// captures the JSON-decoded request body and forwards it
// through the returned channel. The channel buffers one
// element so the test never blocks waiting for the handler.
func captureRequest(t *testing.T) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	ch := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("unmarshal: %v", err)
			return
		}
		ch <- got
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],"model":"m"}`))
	}))
	return srv, ch
}

// inputsOf reads the captured input slice out of the
// captured request body. Returns the slice and whether
// it was present.
func inputsOf(body map[string]any) ([]string, bool) {
	raw, ok := body["input"]
	if !ok {
		return nil, false
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	case string:
		return []string{v}, true
	}
	return nil, false
}

// TestEmbed_DocPromptPrependedToChunkInput pins the
// embedding-gemma optimization: when docPrompt is set,
// every chunk input is prepended with it before being
// POSTed to /v1/embeddings. The prompt is what tells
// the model "this text is a document to be indexed"
// rather than a free-form string — without it,
// retrieval quality drops measurably.
func TestEmbed_DocPromptPrependedToChunkInput(t *testing.T) {
	t.Parallel()

	srv, captured := captureRequest(t)
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "embeddinggemma", "", 5*time.Second, 0, 1,
		"title: none | text:",          // docPrompt
		"task: search result | query:", // queryPrompt
	)

	_, err := client.GenerateEmbedding(context.Background(), "the quick brown fox")
	require.NoError(t, err)

	body := <-captured
	inputs, ok := inputsOf(body)
	require.True(t, ok, "input must be present in the request body")
	require.Len(t, inputs, 1)
	require.Equal(t, "title: none | text:the quick brown fox", inputs[0],
		"docPrompt must be prepended to chunk text exactly as configured")
}

// TestEmbed_EmptyDocPromptIsNoOp pins backward compat:
// an empty docPrompt preserves the historical "raw text"
// behavior, so non-embedding-gemma models keep working
// with no operator action.
func TestEmbed_EmptyDocPromptIsNoOp(t *testing.T) {
	t.Parallel()

	srv, captured := captureRequest(t)
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "nomic-embed-text:v1.5", "", 5*time.Second, 0, 1,
		"", // empty docPrompt
		"", // empty queryPrompt
	)

	_, err := client.GenerateEmbedding(context.Background(), "raw text")
	require.NoError(t, err)

	body := <-captured
	inputs, _ := inputsOf(body)
	require.Equal(t, []string{"raw text"}, inputs,
		"empty prompt must NOT prepend anything")
}

// TestEmbedQuery_UsesQueryPrompt verifies the dedicated
// query-side method: when queryPrompt is set, the
// EmbedQuery entry point prepends it (not the docPrompt).
// Without this split, a query would carry the doc prompt
// and the model would embed it as if it were an indexed
// document — measurably worse retrieval quality.
func TestEmbedQuery_UsesQueryPrompt(t *testing.T) {
	t.Parallel()

	srv, captured := captureRequest(t)
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "embeddinggemma", "", 5*time.Second, 0, 1,
		"title: none | text:",          // docPrompt
		"task: search result | query:", // queryPrompt
	)

	_, err := client.EmbedQuery(context.Background(), "what is ragabast")
	require.NoError(t, err)

	body := <-captured
	inputs, _ := inputsOf(body)
	require.Equal(t, "task: search result | query:what is ragabast", inputs[0])
}

// TestEmbedQuery_EmptyQueryPromptIsNoOp mirrors the
// docPrompt case: empty queryPrompt preserves raw-text
// behavior. Together, the empty-prompt tests let an
// operator disable the feature entirely by clearing the
// env vars (no value defaults to "no prompt").
func TestEmbedQuery_EmptyQueryPromptIsNoOp(t *testing.T) {
	t.Parallel()

	srv, captured := captureRequest(t)
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "nomic-embed-text:v1.5", "", 5*time.Second, 0, 1,
		"", // empty docPrompt
		"", // empty queryPrompt
	)

	_, err := client.EmbedQuery(context.Background(), "raw query")
	require.NoError(t, err)

	body := <-captured
	inputs, _ := inputsOf(body)
	require.Equal(t, []string{"raw query"}, inputs)
}

// TestEmbed_PromptAndMatryoshkaBothApplied pins the
// interaction with the dimensions request field:
// both `dimensions: N` (Matryoshka) and the task prompt
// must land in the same request body. Operators switching
// from nomic-embed-text to embeddinggemma with
// `embedding_dimensions: 256` must keep both working.
func TestEmbed_PromptAndMatryoshkaBothApplied(t *testing.T) {
	t.Parallel()

	srv, captured := captureRequest(t)
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "embeddinggemma", "", 5*time.Second, 256, 1, // Matryoshka 256
		"title: none | text:",
		"task: search result | query:",
	)

	_, err := client.GenerateEmbedding(context.Background(), "hello")
	require.NoError(t, err)

	body := <-captured
	inputs, _ := inputsOf(body)
	require.Equal(t, "title: none | text:hello", inputs[0])
	// Dimensions is sent as a top-level numeric field;
	// JSON-decoded into map[string]any it shows up as
	// float64. Pin the exact value to guard against
	// accidentally dropping the Matryoshka path.
	require.EqualValues(t, 256, body["dimensions"],
		"dimensions must still be sent alongside the prompt")
}

// TestEmbed_PromptsPersistAcrossBatchCalls guards
// against a regression where the prompts get re-read on
// every call (slow). The constructor sets them once;
// subsequent calls re-use the same value.
func TestEmbed_PromptsPersistAcrossBatchCalls(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var captured []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		mu.Lock()
		captured = append(captured, got)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "embeddinggemma", "", 5*time.Second, 0, 1,
		"title: none | text:",
		"task: search result | query:",
	)

	for range 3 {
		_, err := client.GenerateEmbedding(context.Background(), "x")
		require.NoError(t, err)
	}
	for range 3 {
		_, err := client.EmbedQuery(context.Background(), "y")
		require.NoError(t, err)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, captured, 6, "every call must produce one server request")

	// First three: doc prompt. Last three: query prompt.
	for i := range 3 {
		inputs, _ := inputsOf(captured[i])
		require.True(t, len(inputs[0]) > len("title: none | text:") && inputs[0][:len("title: none | text:")] == "title: none | text:",
			"call %d must use doc prompt: %q", i, inputs[0])
	}
	for i := 3; i < 6; i++ {
		inputs, _ := inputsOf(captured[i])
		require.Equal(t, "task: search result | query:", inputs[0][:len("task: search result | query:")],
			"call %d must use query prompt: %q", i, inputs[0])
	}
}
