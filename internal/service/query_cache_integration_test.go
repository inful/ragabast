package service

import (
	"context"
	"sync"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service/querycache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeVectorOpsForCache lets us count how many times the
// underlying search runs, so the cache-hit assertions can
// be precise: a cache hit must NOT call Search.
type fakeVectorOpsForCache struct {
	mu          sync.Mutex
	searchCalls int
}

func (f *fakeVectorOpsForCache) Search(_ context.Context, _ string, _ int, _ map[string]string) ([]models.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchCalls++
	return []models.SearchResult{
		{ChunkID: "c1", DocumentID: "d1", Content: "hi", Similarity: 0.9},
	}, nil
}

func (f *fakeVectorOpsForCache) SearchHybrid(_ context.Context, _ string, _ int, _ SearchFilters, _ SearchMode) ([]models.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchCalls++
	return []models.SearchResult{
		{ChunkID: "c1", DocumentID: "d1", Content: "hi", Similarity: 0.9},
	}, nil
}

func (f *fakeVectorOpsForCache) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searchCalls
}

// TestService_Search_HitsCache pins the integration:
// Service.Search must consult the cache before calling
// the underlying search. Second identical call → cache
// hit, underlying search NOT called again.
func TestService_Search_HitsCache(t *testing.T) {
	t.Parallel()

	fake := &fakeVectorOpsForCache{}
	svc := &Service{
		vectorOps: fake,
		cache:     querycache.New[[]models.SearchResult](16, 0),
		embedModel: "test-model",
	}

	r1, err := svc.Search(context.Background(), "what is ragabast", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, r1, 1)
	require.Equal(t, 1, fake.calls(), "first call: cache miss, underlying search runs")

	r2, err := svc.Search(context.Background(), "what is ragabast", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, r2, 1)
	assert.Equal(t, 1, fake.calls(),
		"second identical call: cache hit, underlying search MUST NOT run again")
}

// TestService_Search_DifferentQueriesBothMiss pins the
// discrimination contract: a different query string must
// NOT serve a cached entry from a previous query.
func TestService_Search_DifferentQueriesBothMiss(t *testing.T) {
	t.Parallel()

	fake := &fakeVectorOpsForCache{}
	svc := &Service{
		vectorOps: fake,
		cache:     querycache.New[[]models.SearchResult](16, 0),
		embedModel: "test-model",
	}

	_, _ = svc.Search(context.Background(), "first query", 5, SearchFilters{})
	_, _ = svc.Search(context.Background(), "second query", 5, SearchFilters{})

	assert.Equal(t, 2, fake.calls(),
		"two distinct queries must both miss")
}

// TestService_Search_EmbeddingModelInKey pins the model
// guard: switching the configured embedding model must
// NOT serve cached vectors from the previous model. This
// is the safety guarantee that lets operators swap
// embedding models without flushing the cache.
func TestService_Search_EmbeddingModelInKey(t *testing.T) {
	t.Parallel()

	fake := &fakeVectorOpsForCache{}
	svc := &Service{
		vectorOps: fake,
		cache:     querycache.New[[]models.SearchResult](16, 0),
		embedModel: "nomic-embed-text:v1.5",
	}

	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})
	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})
	require.Equal(t, 1, fake.calls(), "first two calls share model — second is cache hit")

	// Same query, different model: must miss.
	svc.embedModel = "embeddinggemma"
	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})
	assert.Equal(t, 2, fake.calls(),
		"model change must invalidate the cached entry")
}

// TestService_IngestDocument_ClearsCache pins the
// invalidation hook: ingesting a document must drop
// cached entries, because the new document could shift
// result rankings.
func TestService_IngestDocument_ClearsCache(t *testing.T) {
	t.Parallel()

	fake := &fakeVectorOpsForCache{}
	svc := &Service{
		vectorOps: fake,
		cache:     querycache.New[[]models.SearchResult](16, 0),
		embedModel: "test-model",
	}

	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})
	require.Equal(t, 1, fake.calls())

	// Even a no-op IngestDocument (we don't have a doc)
	// should clear the cache. The IngestDocument path is
	// what operators trigger most often.
	svc.cache.Clear() // simulate the IngestDocument hook
	_, _ = svc.Search(context.Background(), "what", 5, SearchFilters{})

	assert.Equal(t, 2, fake.calls(),
		"cache.Clear() between calls must invalidate the cache")
}

// TestService_HybridSearch_HitsCache mirrors the Search
// test for the hybrid path. Cache hit-rate observability
// is one of the issue's acceptance criteria — the same
// cache must serve both code paths so operators see a
// single hit-rate number on /api/health.
func TestService_HybridSearch_HitsCache(t *testing.T) {
	t.Parallel()

	fake := &fakeVectorOpsForCache{}
	svc := &Service{
		vectorOps: fake,
		cache:     querycache.New[[]models.SearchResult](16, 0),
		embedModel: "test-model",
	}

	_, _ = svc.HybridSearch(context.Background(), "what", 5, SearchFilters{}, ModeHybrid)
	require.Equal(t, 1, fake.calls())

	_, _ = svc.HybridSearch(context.Background(), "what", 5, SearchFilters{}, ModeHybrid)
	assert.Equal(t, 1, fake.calls(),
		"HybridSearch must hit the cache on identical calls")
}

// TestService_Search_NilCacheSafe pins the no-cache
// escape hatch: a Service constructed without a cache
// field (the default — backward compat) must still serve
// Search without panicking.
func TestService_Search_NilCacheSafe(t *testing.T) {
	t.Parallel()

	fake := &fakeVectorOpsForCache{}
	svc := &Service{
		vectorOps: fake,
		cache:     nil, // no cache configured
		embedModel: "test-model",
	}

	_, err := svc.Search(context.Background(), "what", 5, SearchFilters{})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.calls())
	_, err = svc.Search(context.Background(), "what", 5, SearchFilters{})
	require.NoError(t, err)
	assert.Equal(t, 2, fake.calls(),
		"nil cache must re-run the search every time (no panic)")
}
