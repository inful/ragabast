package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestService_QueryDebug_UsesTheCache pins that the chat path
// (POST /chat/message, /api/query, the MCP query tool) consults the
// query cache.
//
// The chat path used to call vectorOps.Search directly: no cache,
// no keyword side, and therefore no exact-term recall. Every other
// retrieval surface went through HybridSearch.
func TestService_QueryDebug_UsesTheCache(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	_, _, err := svc.QueryDebugWithOptions(context.Background(), "what", 5, LLMOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(1), atomic.LoadInt64(calls),
		"first call: cache miss, embeddings server hit")

	_, _, err = svc.QueryDebugWithOptions(context.Background(), "what", 5, LLMOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(1), atomic.LoadInt64(calls),
		"second identical call: cache hit, embeddings server MUST NOT be hit again")
}

// TestService_QueryDebug_SharesTheCacheWithHybridSearch pins that
// the chat path and the search path use the SAME cache entry.
//
// Same query, same limit, same filters — one retrieval, not two.
func TestService_QueryDebug_SharesTheCacheWithHybridSearch(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	_, err := svc.HybridSearch(context.Background(), "what", 5, SearchFilters{}, ModeHybrid)
	require.NoError(t, err)
	require.Equal(t, int64(1), atomic.LoadInt64(calls))

	_, _, err = svc.QueryDebugWithOptions(context.Background(), "what", 5, LLMOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(1), atomic.LoadInt64(calls),
		"the chat path must share the hybrid search cache entry")
}

// TestService_QueryDebug_SourceKindScopeIsInTheCacheKey pins that a
// different source-kind scope does not serve the cached result of
// another scope.
//
// The chat form carries a per-question source-kind filter. Without
// it in the key, "only GitLab issues" would be answered with the
// cached result of the previous, unscoped question.
func TestService_QueryDebug_SourceKindScopeIsInTheCacheKey(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	_, _, err := svc.QueryDebugWithOptions(context.Background(), "what", 5, LLMOptions{
		Filters: SearchFilters{SourceKinds: []models.SourceKind{models.SourceGitLab}},
	})
	require.NoError(t, err)

	_, _, err = svc.QueryDebugWithOptions(context.Background(), "what", 5, LLMOptions{
		Filters: SearchFilters{SourceKinds: []models.SourceKind{models.SourceDocbuilder}},
	})
	require.NoError(t, err)

	require.Equal(t, int64(2), atomic.LoadInt64(calls),
		"a different source-kind scope must miss the cache")
}

// TestService_HybridSearch_DateRangeIsInTheCacheKey pins that the
// date-range filters are part of the cache key.
//
// /api/search accepts created_after / created_before and the filters
// are applied AFTER the retrieval, so two requests that differ only
// in their date range produce different result sets.
func TestService_HybridSearch_DateRangeIsInTheCacheKey(t *testing.T) {
	t.Parallel()

	svc, calls := newCacheTestService(t, 16, 0)

	after := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := svc.HybridSearch(context.Background(), "what", 5,
		SearchFilters{CreatedAfter: &after}, ModeHybrid)
	require.NoError(t, err)
	require.Equal(t, int64(1), atomic.LoadInt64(calls))

	other := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = svc.HybridSearch(context.Background(), "what", 5,
		SearchFilters{CreatedAfter: &other}, ModeHybrid)
	require.NoError(t, err)
	require.Equal(t, int64(2), atomic.LoadInt64(calls),
		"a different date range must miss the cache")
}
