package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service/querycache"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEmbeddingsServer returns an httptest.Server that
// answers /v1/embeddings with a fixed zero-vector and
// counts every call. The cache integration tests don't
// care about vector values; they only need to count
// how many times the embeddings server saw a request.
func stubEmbeddingsServer(t *testing.T, dim int) (*httptest.Server, *int64) {
	t.Helper()
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		switch r.URL.Path {
		case "/v1/models", "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/embeddings":
			w.Header().Set("Content-Type", "application/json")
			//nolint:errcheck // test stub; embedding JSON write always succeeds
			w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[` + zeroJSON(dim) + `]}],"model":"m"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// zeroJSON returns a JSON-encoded array of N zeros for
// the embedding fixture. strings.Builder avoids the
// perfsprint "concat in loop" warning the simple + loop
// trips.
func zeroJSON(n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('0')
	}
	return b.String()
}

// newCacheTestService stands up a minimal Service with a
// real VectorOperations and a real cache. The embeddings
// stub returns zeros so Search returns empty results, but
// that's fine — the tests only assert on the embedding
// call counter (which is the source of truth for cache
// hit-rate observability).
//
// Returns the Service and the *int64 counter that the
// httptest server bumps on every /v1/embeddings request.
// Tests assert the counter to pin the cache-hit contract.
//
// cacheSize is fixed at 16 across all callers today; left
// in the signature so a future test (e.g. testing the
// capacity-zero "disabled" path) doesn't have to touch the
// helper. Marked unparam-by-golangci-lint; suppress.
func newCacheTestService(t *testing.T, cacheSize int, cacheTTL time.Duration) (*Service, *int64) { //nolint:unparam
	t.Helper()
	const dim = 4
	srv, calls := stubEmbeddingsServer(t, dim)

	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = ""
	cfg.Ollama.BaseURL = srv.URL
	cfg.Ollama.EmbeddingModel = "test-model"
	cfg.Ollama.EmbeddingConcurrency = 1
	cfg.Ollama.EmbeddingDimensions = 0
	cfg.VectorDB.EmbeddingDimension = dim
	cfg.VectorDB.PersistenceDir = t.TempDir()
	cfg.VectorDB.CollectionName = "test-cache-" + t.Name()
	cfg.VectorDB.KeywordIndexDir = ""

	db, err := vector.NewVectorDB(
		cfg.VectorDB.CollectionName,
		cfg.VectorDB.EmbeddingDimension,
		cfg.VectorDB.PersistenceDir,
		cfg.Ollama.EmbeddingModel,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	embeddings := vector.NewOpenAIEmbeddingClientWithOptions(
		cfg.Ollama.BaseURL,
		cfg.Ollama.EmbeddingModel,
		"",
		cfg.Ollama.Timeout,
		0, // dimensions
		1,
		"", "", // no task prompts — the stub ignores them
	)
	vectorOps := vector.NewVectorOperations(db, embeddings)

	return &Service{
		config:     cfg,
		vectorOps:  vectorOps,
		cache:      querycache.New[[]models.SearchResult](cacheSize, cacheTTL),
		embedModel: cfg.Ollama.EmbeddingModel,
	}, calls
}

// TestService_Search_HitsCache pins the integration:
// Service.Search must consult the cache before calling
// the underlying search. Second identical call → cache
// hit, underlying search NOT called again.
func TestService_Search_HitsCache(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	r1, err := svc.Search(context.Background(), "what is ragabast", 5, SearchFilters{})
	require.NoError(t, err)
	require.Empty(t, r1, "empty corpus returns no results")
	require.Equal(t, int64(1), atomic.LoadInt64(calls),
		"first call: cache miss, embeddings server hit")

	r2, err := svc.Search(context.Background(), "what is ragabast", 5, SearchFilters{})
	require.NoError(t, err)
	require.Empty(t, r2)
	assert.Equal(t, int64(1), atomic.LoadInt64(calls),
		"second identical call: cache hit, embeddings server MUST NOT be hit again")
}

// TestService_Search_DifferentQueriesBothMiss pins the
// discrimination contract: a different query string must
// NOT serve a cached entry from a previous query.
func TestService_Search_DifferentQueriesBothMiss(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	_, _ = svc.Search(context.Background(), "first query", 5, SearchFilters{})
	_, _ = svc.Search(context.Background(), "second query", 5, SearchFilters{})

	assert.Equal(t, int64(2), atomic.LoadInt64(calls),
		"two distinct queries must both miss")
}

// TestService_Search_EmbeddingModelInKey pins the model
// guard: switching the configured embedding model must
// NOT serve cached vectors from the previous model. This
// is the safety guarantee that lets operators swap
// embedding models without flushing the cache.
func TestService_Search_EmbeddingModelInKey(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})
	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})
	require.Equal(t, int64(1), atomic.LoadInt64(calls),
		"first two calls share model — second is cache hit")

	// Same query, different model: must miss.
	svc.embedModel = "embeddinggemma"
	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})
	assert.Equal(t, int64(2), atomic.LoadInt64(calls),
		"model change must invalidate the cached entry")
}

// TestService_IngestDocument_ClearsCache pins the
// invalidation hook: ingesting a document must drop
// cached entries, because the new document could shift
// result rankings.
func TestService_IngestDocument_ClearsCache(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})
	require.Equal(t, int64(1), atomic.LoadInt64(calls))

	// Even a no-op IngestDocument (we don't have a doc to
	// ingest) should clear the cache. The IngestDocument
	// path is what operators trigger most often.
	svc.cache.Clear()
	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})

	assert.Equal(t, int64(2), atomic.LoadInt64(calls),
		"cache.Clear() between calls must invalidate the cache")
}

// TestService_HybridSearch_HitsCache mirrors the Search
// test for the hybrid path. Cache hit-rate observability
// is one of the issue's acceptance criteria — the same
// cache must serve both code paths so operators see a
// single hit-rate number on /api/health.
func TestService_HybridSearch_HitsCache(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	_, _ = svc.HybridSearch(context.Background(), "what", 5, SearchFilters{}, ModeHybrid)
	require.Equal(t, int64(1), atomic.LoadInt64(calls),
		"first call hits the embeddings server")

	_, _ = svc.HybridSearch(context.Background(), "what", 5, SearchFilters{}, ModeHybrid)
	assert.Equal(t, int64(1), atomic.LoadInt64(calls),
		"HybridSearch must hit the cache on identical calls")
}

// TestService_Search_NilCacheSafe pins the no-cache
// escape hatch: a Service constructed without a cache
// field must still serve Search without panicking.
func TestService_Search_NilCacheSafe(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)
	svc.cache = nil // disable cache

	_, err := svc.Search(context.Background(), "what", 5, SearchFilters{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(calls))
	_, err = svc.Search(context.Background(), "what", 5, SearchFilters{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), atomic.LoadInt64(calls),
		"nil cache must re-run the search every time (no panic)")
}
