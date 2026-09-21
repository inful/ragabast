package vector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateChunkEmbeddings_ConcurrentCalls pins the headline
// behavior of issue #16: with Concurrency=N, the client issues
// up to N parallel HTTP requests instead of one giant request.
// The fake server below counts concurrent in-flight requests
// so the test can assert that the configured concurrency
// was actually used.
func TestGenerateChunkEmbeddings_ConcurrentCalls(t *testing.T) {
	const (
		concurrency = 4
		chunkCount  = 16
	)

	// Atomic counter that tracks in-flight HTTP requests.
	var inFlight atomic.Int64
	var peakInFlight atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		// Track the highest concurrent value seen.
		for {
			prev := peakInFlight.Load()
			if cur <= prev || peakInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		// Each request takes 50ms — long enough that all
		// parallel workers are in flight simultaneously when
		// concurrency is correctly applied.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]},
			{"object":"embedding","index":1,"embedding":[0.4,0.5,0.6]},
			{"object":"embedding","index":2,"embedding":[0.7,0.8,0.9]},
			{"object":"embedding","index":3,"embedding":[0.1,0.1,0.1]}
		]}`))
	}))
	defer srv.Close()

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "test-model", "", 5*time.Second, 3, concurrency,
		"", "")

	chunks := make([]*models.Chunk, chunkCount)
	for i := range chunks {
		chunks[i] = &models.Chunk{ID: fmt.Sprintf("c%d", i), DocumentID: "doc-1"}
	}

	start := time.Now()
	out, err := client.GenerateChunkEmbeddings(context.Background(), chunks)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Len(t, out, chunkCount)

	// With concurrency=4, peak in-flight should reach 4. (One
	// sub-batch per parallel worker; the 16 chunks split into
	// 4 sub-batches of 4 each.)
	peak := peakInFlight.Load()
	assert.GreaterOrEqual(t, peak, int64(concurrency),
		"peak in-flight requests must reach the configured concurrency; got %d", peak)

	// 16 chunks at 50ms each, 4-way concurrency → ~200ms
	// total. Sequential would be ~800ms.
	assert.Less(t, elapsed, 500*time.Millisecond,
		"parallel execution must beat sequential; elapsed=%s", elapsed)
}

// TestGenerateChunkEmbeddings_PreservesOrder pins that the
// embeddings returned by the parallel pool are still in the
// same order as the input. Callers index by chunk position;
// reordering would silently corrupt the vector store.
//
// The fake server returns each input's first byte as its
// embedding vector — so a chunk containing "alpha" gets an
// embedding of [97, ...], "beta" gets [98, ...], etc. Any
// reorder in the worker pool would put the wrong embedding
// at the wrong chunk position and the assertion below would
// catch it.
func TestGenerateChunkEmbeddings_PreservesOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var inputs []string
		if err := json.Unmarshal(body.Input, &inputs); err != nil {
			var single string
			if err2 := json.Unmarshal(body.Input, &single); err2 != nil {
				http.Error(w, "input is not a string or array", http.StatusBadRequest)
				return
			}
			inputs = []string{single}
		}
		w.Header().Set("Content-Type", "application/json")
		var buf strings.Builder
		buf.WriteString(`{"object":"list","data":[`)
		for i, in := range inputs {
			if i > 0 {
				buf.WriteByte(',')
			}
			// Embedding[0] is the first byte of the input so
			// the test can verify "this chunk got THIS
			// response". We append 0,0,0 just to keep a 4-wide
			// shape — only [0] is inspected by the test.
			fmt.Fprintf(&buf, `{"object":"embedding","index":%d,"embedding":[`, i)
			if len(in) > 0 {
				fmt.Fprintf(&buf, "%d", in[0])
			} else {
				buf.WriteString("0")
			}
			buf.WriteString(`,0,0,0]}`)
		}
		buf.WriteString(`]}`)
		_, _ = w.Write([]byte(buf.String()))
	}))
	defer srv.Close()

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "test-model", "", 5*time.Second, 4, 3,
		"", "")

	chunks := []*models.Chunk{
		{ID: "c0", DocumentID: "doc-1", Content: "alpha"},
		{ID: "c1", DocumentID: "doc-1", Content: "beta"},
		{ID: "c2", DocumentID: "doc-1", Content: "gamma"},
	}
	out, err := client.GenerateChunkEmbeddings(context.Background(), chunks)
	require.NoError(t, err)
	require.Len(t, out, 3)

	// Each chunk's first byte maps to its expected embedding[0].
	// 'a' = 97, 'b' = 98, 'g' = 103. Any reorder would put the
	// wrong byte at the wrong position.
	assert.InDelta(t, float32('a'), out[0][0], 0.001,
		"chunk 0 ('alpha') must keep its identity in position 0")
	assert.InDelta(t, float32('b'), out[1][0], 0.001,
		"chunk 1 ('beta') must keep its identity in position 1")
	assert.InDelta(t, float32('g'), out[2][0], 0.001,
		"chunk 2 ('gamma') must keep its identity in position 2")
}

// TestGenerateChunkEmbeddings_ErrorFromOneFailsAll pins the
// failure-mode contract from the issue's acceptance criteria:
// an error from any one sub-batch fails the whole call. This
// matches the prior sequential behavior.
func TestGenerateChunkEmbeddings_ErrorFromOneFailsAll(t *testing.T) {
	// Server returns 500 on the second sub-batch and 200 on
	// the others; the worker pool must surface the error and
	// not return a partial result.
	var callCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 2 {
			http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"object":"embedding","index":0,"embedding":[0.1,0.1,0.1]}
		]}`))
	}))
	defer srv.Close()

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "test-model", "", 5*time.Second, 3, 3,
		"", "")

	chunks := []*models.Chunk{
		{ID: "c0", DocumentID: "doc-1"},
		{ID: "c1", DocumentID: "doc-1"},
		{ID: "c2", DocumentID: "doc-1"},
	}
	out, err := client.GenerateChunkEmbeddings(context.Background(), chunks)
	require.Error(t, err,
		"an error from any sub-batch must fail the whole call")
	assert.Nil(t, out,
		"no partial result must be returned on error")
}

// TestGenerateChunkEmbeddings_ConcurrencyOneIsSequential pins
// the boundary case: Concurrency=1 (or 0) preserves the prior
// sequential behavior. Operators running on a server with no
// parallelism headroom can disable the pool entirely.
func TestGenerateChunkEmbeddings_ConcurrencyOneIsSequential(t *testing.T) {
	var inFlight atomic.Int64
	var peakInFlight atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			prev := peakInFlight.Load()
			if cur <= prev || peakInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)

		// Echo one vector per input — the client enforces a
		// strict input/vector count match.
		var body struct {
			Input json.RawMessage `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var inputs []string
		if err := json.Unmarshal(body.Input, &inputs); err != nil {
			var single string
			if err2 := json.Unmarshal(body.Input, &single); err2 != nil {
				http.Error(w, "input is not a string or array", http.StatusBadRequest)
				return
			}
			inputs = []string{single}
		}
		w.Header().Set("Content-Type", "application/json")
		var buf strings.Builder
		buf.WriteString(`{"object":"list","data":[`)
		for i := range inputs {
			if i > 0 {
				buf.WriteByte(',')
			}
			fmt.Fprintf(&buf, `{"object":"embedding","index":%d,"embedding":[0.1,0.2,0.3]}`, i)
		}
		buf.WriteString(`]}`)
		_, _ = w.Write([]byte(buf.String()))
	}))
	defer srv.Close()

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "test-model", "", 5*time.Second, 3, 1,
		"", "")

	chunks := make([]*models.Chunk, 6)
	for i := range chunks {
		chunks[i] = &models.Chunk{ID: fmt.Sprintf("c%d", i), DocumentID: "doc-1"}
	}
	_, err := client.GenerateChunkEmbeddings(context.Background(), chunks)
	require.NoError(t, err)

	// With Concurrency=1, peak in-flight must stay at 1 — the
	// pool processes sub-batches one at a time.
	assert.Equal(t, int64(1), peakInFlight.Load(),
		"Concurrency=1 must run sub-batches sequentially; peak=%d", peakInFlight.Load())
}

// TestGenerateChunkEmbeddings_ContextCanceled pins that the
// worker pool respects ctx cancellation — important because
// a slow embedding server should not stall an HTTP handler's
// request lifetime indefinitely. The fake server sleeps past
// the test's ctx deadline; the client must return an error
// rather than waiting for the server to respond.
func TestGenerateChunkEmbeddings_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep long enough that the test's 100ms ctx deadline
		// fires first. The client must give up on its own; the
		// server doesn't observe the cancel here.
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.CloseClientConnections() // force-close in-flight handlers when the test exits

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "test-model", "", 5*time.Second, 3, 2,
		"", "")

	chunks := []*models.Chunk{{ID: "c0", DocumentID: "doc-1"}}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.GenerateChunkEmbeddings(ctx, chunks)
	require.Error(t, err,
		"a context that times out must surface as an error")
	// Don't pin the exact error type — the HTTP client may
	// return a wrapped timeout, EOF, or connection-reset error
	// depending on Go version and timing. The contract is: failed.
	assert.True(t,
		errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, context.Canceled) ||
			strings.Contains(err.Error(), "context deadline exceeded") ||
			strings.Contains(err.Error(), "context canceled") ||
			strings.Contains(err.Error(), "EOF") ||
			strings.Contains(err.Error(), "connection reset"),
		"error must indicate cancellation/timeout/EOF; got: %v", err)
}
