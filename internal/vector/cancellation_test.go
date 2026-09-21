package vector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateChunkEmbeddings_CancellationAbortsInFlight pins
// the headline behavior of issue #31: cancel the context
// while embeddings are in flight aborts the call promptly.
// The fake server delays each request past the test's ctx
// deadline, so the only way the call can return within the
// deadline is for the client to honor ctx cancellation.
func TestGenerateChunkEmbeddings_CancellationAbortsInFlight(t *testing.T) {
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
		// Block past the test's ctx timeout. The test asserts the
		// client gives up well before this returns.
		select {
		case <-time.After(5 * time.Second):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[
				{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}
			]}`))
		case <-r.Context().Done():
			return
		}
	}))
	defer srv.CloseClientConnections()
	defer srv.Close()

	client := NewOpenAIEmbeddingClientWithOptions(
		srv.URL, "test-model", "", 5*time.Second, 3, 4,
	)

	// 16 chunks × ~5s latency each, 4-way parallel = the
	// sequential total would be ~80s; with parallelism and
	// proper cancellation, the test expects to bail within
	// ~1s.
	texts := make([]string, 16)
	for i := range texts {
		texts[i] = "c"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.generateEmbeddingsBatch(ctx, texts)
	elapsed := time.Since(start)
	t.Logf("generateEmbeddingsBatch returned in %s with err=%v", elapsed, err)

	require.Error(t, err,
		"a context that times out must surface as an error")
	assert.Less(t, elapsed, 1*time.Second,
		"cancellation must abort the call well before the 5s server timeout; elapsed=%s", elapsed)
}
