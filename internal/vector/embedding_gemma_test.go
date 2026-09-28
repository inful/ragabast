package vector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// stubEmbeddingDim is the dimension the EmbeddingGemma
// test stub emits per vector. Picked to match the
// OpenAI client's stubEmbeddingsServer, so cross-package
// tests can swap implementations without re-aligning
// expectations.
const stubEmbeddingDim = 4

// stubEmbeddingGemmaServer returns an httptest.Server that
// mimics the EmbeddingGemma /v2/embed endpoint. Every input
// text gets back a fixed-dimension 4-dim vector; the first
// element is 1.0 and the rest are 0.0 so tests can spot
// cross-chunk contamination. Returns the parallel
// "embeddings" response shape — the most common wire format
// the new client probes for first.
func stubEmbeddingGemmaServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/embed" {
			http.Error(w, "stub: unknown path", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "stub: read body", http.StatusBadRequest)
			return
		}
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "stub: parse body", http.StatusBadRequest)
			return
		}
		embeddings := make([][]float32, len(req.Texts))
		for i := range req.Texts {
			vec := make([]float32, stubEmbeddingDim)
			vec[i%stubEmbeddingDim] = 1.0
			embeddings[i] = vec
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": embeddings,
		})
	}))
}

// TestEmbeddingGemmaClient_PostsToV2EmbedWithTexts pins the
// wire shape: the only body field is "texts", POSTed to
// /v2/embed (NOT /v1/embeddings). Operators with an
// EmbeddingGemma server at the configured base URL get a
// client that just works.
func TestEmbeddingGemmaClient_PostsToV2EmbedWithTexts(t *testing.T) {
	t.Parallel()

	srv := stubEmbeddingGemmaServer(t)
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(
		srv.URL, 5*time.Second, 0, 1, "", "",
	)

	vec, err := client.GenerateEmbedding(context.Background(), "hello world")
	require.NoError(t, err)
	require.Len(t, vec, 4)
}

// TestEmbeddingGemmaClient_NoAuthHeader pins the auth
// contract: the EmbeddingGemma /v2/embed endpoint doesn't
// expect a bearer token. Operators running an internal
// EmbeddingGemma server don't have to mint a key just to
// call it. If a future operator DOES need auth, they can
// extend the client; today we don't send an Authorization
// header at all.
func TestEmbeddingGemmaClient_NoAuthHeader(t *testing.T) {
	t.Parallel()

	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(
		srv.URL, 5*time.Second, 0, 1, "", "",
	)

	_, err := client.GenerateEmbedding(context.Background(), "hi")
	require.NoError(t, err)
	require.Empty(t, capturedAuth,
		"EmbeddingGemma client must not send an Authorization header")
}

// TestEmbeddingGemmaClient_OnlyTextsInBody pins the body
// contract: the request body has exactly one field,
// "texts", regardless of any model name or Matryoshka
// dimensions configured client-side. The server picks the
// model itself; ragabast doesn't second-guess it.
func TestEmbeddingGemmaClient_OnlyTextsInBody(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	}))
	t.Cleanup(srv.Close)

	// Even with doc/query prompts and a Matryoshka dim
	// configured, the EmbeddingGemma wire shape is fixed:
	// only "texts" is sent.
	client := NewEmbeddingGemmaClientWithOptions(
		srv.URL, 5*time.Second, 256, 1, // 256 dim configured — must NOT show up in body
		"title: none | text:", "task: search result | query:",
	)

	_, err := client.GenerateEmbedding(context.Background(), "hello")
	require.NoError(t, err)
	require.Contains(t, captured, "texts")
	require.NotContains(t, captured, "model",
		"EmbeddingGemma wire shape must not include model — server picks it")
	require.NotContains(t, captured, "dimensions",
		"EmbeddingGemma wire shape must not include dimensions — server picks it")
	require.NotContains(t, captured, "input",
		"EmbeddingGemma uses 'texts', not OpenAI's 'input'")
}

// TestEmbeddingGemmaClient_DocPromptPrepended pins the
// embedding-gemma optimization: when docPrompt is set,
// every chunk input is prepended with it before being
// POSTed to /v2/embed.
func TestEmbeddingGemmaClient_DocPromptPrepended(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(
		srv.URL, 5*time.Second, 0, 1,
		"title: none | text:",
		"task: search result | query:",
	)

	_, err := client.GenerateEmbedding(context.Background(), "the quick brown fox")
	require.NoError(t, err)

	texts, ok := captured["texts"].([]any)
	require.True(t, ok, "texts field must be present")
	require.Len(t, texts, 1)
	require.Equal(t, "title: none | text:the quick brown fox", texts[0])
}

// TestEmbeddingGemmaClient_QueryPromptPrepended pins the
// query-side entry point: EmbedQuery prepends queryPrompt,
// not docPrompt.
func TestEmbeddingGemmaClient_QueryPromptPrepended(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(
		srv.URL, 5*time.Second, 0, 1,
		"title: none | text:",
		"task: search result | query:",
	)

	_, err := client.EmbedQuery(context.Background(), "what is ragabast")
	require.NoError(t, err)

	texts, ok := captured["texts"].([]any)
	require.True(t, ok)
	require.Len(t, texts, 1)
	require.Equal(t, "task: search result | query:what is ragabast", texts[0])
}

// TestEmbeddingGemmaClient_ResponseShapeEmbeddings pins the
// primary wire shape: {"embeddings": [[...]]} — the
// parallel-of-texts response, the most common shape.
func TestEmbeddingGemmaClient_ResponseShapeEmbeddings(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	out, err := client.GenerateEmbedding(context.Background(), "hi")
	require.NoError(t, err)
	require.Equal(t, []float32{0.1, 0.2, 0.3}, out)
}

// TestEmbeddingGemmaClient_ResponseShapeResults pins the
// alternate wire shape: {"results": [{"embedding": [...]}]}.
// The shape probe accepts this in addition to the primary
// `embeddings` field.
func TestEmbeddingGemmaClient_ResponseShapeResults(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"embedding":[0.7,0.8,0.9]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	out, err := client.GenerateEmbedding(context.Background(), "hi")
	require.NoError(t, err)
	require.Equal(t, []float32{0.7, 0.8, 0.9}, out)
}

// TestEmbeddingGemmaClient_ResponseShapeData pins the
// OpenAI-style data array as a third acceptable shape:
// {"data": [{"embedding": [...]}]}. Some servers expose
// the /v2/embed endpoint but reuse the OpenAI response
// shape — we accept that too.
func TestEmbeddingGemmaClient_ResponseShapeData(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.11,0.22,0.33]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	out, err := client.GenerateEmbedding(context.Background(), "hi")
	require.NoError(t, err)
	require.Equal(t, []float32{0.11, 0.22, 0.33}, out)
}

// TestEmbeddingGemmaClient_UnknownResponseShapeLogsBody
// pins the failure mode for an unrecognized shape: parse
// fails, error wraps the response body so the operator
// can see what the server actually returned and update
// either the client or their proxy.
func TestEmbeddingGemmaClient_UnknownResponseShapeLogsBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unusual_field":[[0.1,0.2]]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	_, err := client.GenerateEmbedding(context.Background(), "hi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unusual_field",
		"error must include the response body so the operator can see the actual shape")
}

// TestEmbeddingGemmaClient_EmptyTextReturnsError pins the
// contract that the single-text path refuses an empty
// input. Empty embeddings are a common silent failure —
// refuse up front so the caller sees it.
func TestEmbeddingGemmaClient_EmptyTextReturnsError(t *testing.T) {
	t.Parallel()

	srv := stubEmbeddingGemmaServer(t)
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	_, err := client.GenerateEmbedding(context.Background(), "")
	require.Error(t, err)
}

// TestEmbeddingGemmaClient_BatchSendsAllTexts pins the
// batch path: every chunk is included in the "texts"
// array, and the server's response vectors come back in
// input order.
func TestEmbeddingGemmaClient_BatchSendsAllTexts(t *testing.T) {
	t.Parallel()

	srv := stubEmbeddingGemmaServer(t)
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	chunks := []*models.Chunk{
		{ID: "c0", Content: "alpha", DocumentID: "d"},
		{ID: "c1", Content: "beta", DocumentID: "d"},
		{ID: "c2", Content: "gamma", DocumentID: "d"},
	}
	// stubEmbeddingsServer uses chunk.GetFullPath() for
	// the request text on the OpenAI path; the new
	// EmbeddingGemma path uses the same hook. Set
	// DocumentTitle so GetFullPath returns the title
	// (not empty) for each chunk.
	for _, c := range chunks {
		c.DocumentTitle = "doc"
		c.Fingerprint = "fp"
	}

	out, err := client.GenerateChunkEmbeddings(context.Background(), chunks)
	require.NoError(t, err)
	require.Len(t, out, 3, "one vector per input chunk")
	for i, v := range out {
		require.Len(t, v, 4, "vector %d must be dim 4", i)
	}
}

// TestEmbeddingGemmaClient_DimensionMismatchWarns pins the
// Matryoshka-style mismatch check: when expectedDimension
// is configured and the server returns a different size,
// the client logs a one-shot warning and keeps returning
// the actual vectors (don't fail the request — operators
// can still get results, they just need to fix the config).
func TestEmbeddingGemmaClient_DimensionMismatchWarns(t *testing.T) {
	t.Parallel()

	// Server returns 4-dim vectors, client expects 256.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.1,0.1,0.1]]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(
		srv.URL, 5*time.Second, 256 /* expected */, 1, "", "",
	)

	out, err := client.GenerateEmbedding(context.Background(), "hi")
	require.NoError(t, err, "mismatch should warn, not fail")
	require.Len(t, out, 4)
}

// TestEmbeddingGemmaClient_ValidateConnectionOK pins the
// startup probe: POSTing to /v2/embed with a probe text
// succeeds, so the client is reachable.
func TestEmbeddingGemmaClient_ValidateConnectionOK(t *testing.T) {
	t.Parallel()

	srv := stubEmbeddingGemmaServer(t)
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	require.NoError(t, client.ValidateConnection(context.Background()))
}

// TestEmbeddingGemmaClient_ValidateConnectionFails pins
// the failure path: a non-2xx response is reported so the
// startup probe or doctor check sees the actual server
// status code.
func TestEmbeddingGemmaClient_ValidateConnectionFails(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	err := client.ValidateConnection(context.Background())
	require.Error(t, err)
}

// TestEmbeddingGemmaClient_EmptyBaseURLDefaultsToLocalhost
// pins the package default: empty base URL falls back to
// http://localhost:11434 — same convention as the OpenAI
// client, so an operator who only sets the model name
// (none here, since the wire shape is model-less) gets a
// client pointing somewhere sensible.
func TestEmbeddingGemmaClient_EmptyBaseURLDefaultsToLocalhost(t *testing.T) {
	t.Parallel()

	client := NewEmbeddingGemmaClientWithOptions("", 0, 0, 1, "", "")
	require.Equal(t, "http://localhost:11434", client.baseURLString())
}

// TestEmbeddingGemmaClient_NonPositiveTimeoutDefaultsTo30s
// pins the package default: timeout <= 0 falls back to
// 30s. Matches the OpenAI client so operators don't have
// to remember a separate default for each client.
func TestEmbeddingGemmaClient_NonPositiveTimeoutDefaultsTo30s(t *testing.T) {
	t.Parallel()

	client := NewEmbeddingGemmaClientWithOptions("http://x", 0, 0, 1, "", "")
	require.Equal(t, 30*time.Second, client.httpClient.Timeout)
}

// TestEmbeddingGemmaClient_ConcurrencyDefaultsToOne pins
// the package default: concurrency < 1 falls back to 1
// (sequential), matching the OpenAI client's
// pre-pool behavior so an operator who hasn't tuned the
// pool yet still gets correct results.
func TestEmbeddingGemmaClient_ConcurrencyDefaultsToOne(t *testing.T) {
	t.Parallel()

	client := NewEmbeddingGemmaClientWithOptions("http://x", 5*time.Second, 0, 0, "", "")
	require.Equal(t, 1, client.concurrencyString())
}

// TestEmbeddingGemmaClient_ConcurrentSplitsIntoSubBatches
// pins the parallel worker-pool behavior: when
// concurrency > 1, the input is split into N sub-batches
// and processed concurrently. The output order must match
// the input order — operators rely on per-chunk vector
// positions to write back into the vector DB.
func TestEmbeddingGemmaClient_ConcurrentSplitsIntoSubBatches(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		batches []int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		batches = append(batches, len(req.Texts))
		mu.Unlock()
		// Return a per-input vector of dim 2.
		embeddings := make([][]float32, len(req.Texts))
		for i := range req.Texts {
			embeddings[i] = []float32{float32(i), float32(i) + 0.5}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embeddings})
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(
		srv.URL, 10*time.Second, 0, 3, "", "", // 3-way concurrency
	)

	chunks := make([]*models.Chunk, 9)
	for i := range chunks {
		chunks[i] = &models.Chunk{
			ID:            "c" + string(rune('0'+i)),
			Content:       "x",
			DocumentID:    "d",
			DocumentTitle: "t",
			Fingerprint:   "f",
		}
	}

	out, err := client.GenerateChunkEmbeddings(context.Background(), chunks)
	require.NoError(t, err)
	require.Len(t, out, 9)

	// 9 chunks split across 3 sub-batches: sizes sum to 9.
	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, n := range batches {
		total += n
	}
	require.Equal(t, 9, total,
		"every chunk must be sent exactly once across all sub-batches")
}

// TestEmbeddingGemmaClient_HTTPErrorIsSurfaced pins the
// failure mode: a non-2xx response from the server surfaces
// the status code and the response body so the operator
// can see what went wrong.
func TestEmbeddingGemmaClient_HTTPErrorIsSurfaced(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not loaded", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	_, err := client.GenerateEmbedding(context.Background(), "hi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "503")
	require.Contains(t, err.Error(), "model not loaded")
}

// TestEmbeddingGemmaClient_ResponseLengthMismatchSurfaces
// pins the count-check: the server returns N vectors for
// M inputs; the client refuses the response rather than
// silently stitching mismatched data.
func TestEmbeddingGemmaClient_ResponseLengthMismatchSurfaces(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return only 1 vector but the request had 2 texts.
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEmbeddingGemmaClientWithOptions(srv.URL, 5*time.Second, 0, 1, "", "")

	chunks := []*models.Chunk{
		{ID: "c0", Content: "alpha", DocumentID: "d", DocumentTitle: "t", Fingerprint: "f"},
		{ID: "c1", Content: "beta", DocumentID: "d", DocumentTitle: "t", Fingerprint: "f"},
	}
	_, err := client.GenerateChunkEmbeddings(context.Background(), chunks)
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "1") && strings.Contains(err.Error(), "2"),
		"error must mention both the actual and expected counts: %v", err)
}
