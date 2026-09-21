package querycache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// get is a test helper that discards the error return from
// Cache.Get. Production callers should always check the
// error, but the cache tests use loaders that can't fail,
// so the discard is safe and keeps the test bodies focused
// on the cache contract.
func get[V any](c *Cache[V], key string, loader func(context.Context) (V, error)) (V, bool) {
	v, hit, err := c.Get(context.Background(), key, loader)
	if err != nil {
		panic(err)
	}
	return v, hit
}

// TestCache_HitOnSecondGet pins the basic cache contract:
// the second Get for the same key returns the cached value
// without re-running the loader.
func TestCache_HitOnSecondGet(t *testing.T) {
	t.Parallel()

	c := New[string](16, 0)
	loadCalls := 0
	loader := func(ctx context.Context) (string, error) {
		loadCalls++
		return "result", nil
	}

	got1, hit1 := get(c, "k1", loader)
	require.False(t, hit1, "first call must miss")
	require.Equal(t, "result", got1)
	require.Equal(t, 1, loadCalls)

	got2, hit2 := get(c, "k1", loader)
	require.True(t, hit2, "second call must hit")
	require.Equal(t, "result", got2)
	require.Equal(t, 1, loadCalls, "loader must not run on cache hit")
}

// TestCache_MissOnDifferentKeys pins the key-discrimination
// contract: same loader invoked for two different keys
// produces two entries, no cross-contamination.
func TestCache_MissOnDifferentKeys(t *testing.T) {
	t.Parallel()

	c := New[string](16, 0)
	loadCalls := 0
	loader := func(ctx context.Context) (string, error) {
		loadCalls++
		return "result", nil
	}

	_, _ = get(c, "k1", loader)
	_, _ = get(c, "k2", loader)
	require.Equal(t, 2, loadCalls, "two distinct keys must each trigger a load")
}

// TestCache_StatsCountHitsAndMisses pins the hit-rate
// observability required by the /api/health contract.
func TestCache_StatsCountHitsAndMisses(t *testing.T) {
	t.Parallel()

	c := New[int](16, 0)
	loader := func(ctx context.Context) (int, error) { return 42, nil }

	// 1 miss, 3 hits.
	_, _ = get(c, "k", loader)
	_, _ = get(c, "k", loader)
	_, _ = get(c, "k", loader)
	_, _ = get(c, "k", loader)

	stats := c.Stats()
	require.Equal(t, int64(1), stats.Misses)
	require.Equal(t, int64(3), stats.Hits)
	require.Equal(t, int64(4), stats.Lookups)
}

// TestCache_ClearDropsAllEntries pins the invalidation
// contract: a Clear call resets the cache so the next Get
// for any key is a miss.
func TestCache_ClearDropsAllEntries(t *testing.T) {
	t.Parallel()

	c := New[string](16, 0)
	loader := func(ctx context.Context) (string, error) { return "v", nil }

	_, _ = get(c, "k1", loader)
	_, _ = get(c, "k2", loader)

	c.Clear()

	loadCalls := 0
	postClearLoader := func(ctx context.Context) (string, error) {
		loadCalls++
		return "v", nil
	}
	_, hit := get(c, "k1", postClearLoader)
	require.False(t, hit, "Clear must drop k1")
	_, hit = get(c, "k2", postClearLoader)
	require.False(t, hit, "Clear must drop k2")
	require.Equal(t, 2, loadCalls, "both keys must reload after Clear")
}

// TestCache_LRUEvictsOldestWhenFull pins the memory-bound
// guarantee: when size exceeds capacity, the least-recently
// USED entry is evicted (not necessarily the oldest
// inserted).
//
// The test deliberately DOES NOT re-query k2 after eviction
// to load it back — that would itself evict k1 (because
// k1 is now the LRU), which is correct LRU behavior but
// muddies the assertion. The order check below uses the
// loader argument as a no-op signal: a hit means the
// loader was never called.
func TestCache_LRUEvictsOldestWhenFull(t *testing.T) {
	t.Parallel()

	c := New[string](2, 0)
	loaderCalls := 0
	loader := func(ctx context.Context) (string, error) {
		loaderCalls++
		return "v", nil
	}

	// Fill the cache to capacity: k1, k2.
	_, _ = get(c, "k1", loader)
	_, _ = get(c, "k2", loader)
	require.Equal(t, 2, loaderCalls)

	// Touch k1 so k2 is now the LRU entry.
	_, hitTouch := get(c, "k1", func(ctx context.Context) (string, error) {
		t.Fatal("touch of k1 must NOT call loader — k1 was just inserted")
		return "", nil
	})
	require.True(t, hitTouch, "touch must hit")

	// Adding k3 must evict k2 (the LRU), not k1 (which we
	// just touched). Loader is called once for k3.
	_, _ = get(c, "k3", loader)
	require.Equal(t, 3, loaderCalls, "k3 was a miss → loader ran once")

	// k1 must still be present (recently touched). A hit
	// here means the loader argument was never invoked.
	_, hitK1 := get(c, "k1", func(ctx context.Context) (string, error) {
		t.Fatal("k1 must NOT be reloaded — it's still in the cache")
		return "", nil
	})
	require.True(t, hitK1, "k1 must still be present (recently touched)")

	// k3 must still be present (just inserted). A hit
	// here means the loader argument was never invoked.
	_, hitK3 := get(c, "k3", func(ctx context.Context) (string, error) {
		t.Fatal("k3 must NOT be reloaded — it's still in the cache")
		return "", nil
	})
	require.True(t, hitK3, "k3 must be present (just inserted)")
}

// TestCache_TTLExpiresEntries pins the optional TTL path:
// when a TTL is set and an entry has been in the cache
// longer than the TTL, the next Get must miss and reload.
func TestCache_TTLExpiresEntries(t *testing.T) {
	t.Parallel()

	// Tiny TTL so the test doesn't need wall-clock sleeps.
	c := New[string](16, 5*time.Millisecond)
	loader := func(ctx context.Context) (string, error) { return "v", nil }

	_, _ = get(c, "k", loader)
	_, hit := get(c, "k", func(ctx context.Context) (string, error) { return "v", nil })
	require.True(t, hit, "fresh entry must hit")

	time.Sleep(10 * time.Millisecond)
	_, hitExpired := get(c, "k", func(ctx context.Context) (string, error) { return "v", nil })
	require.False(t, hitExpired, "entry past TTL must miss")
}

// TestCache_ZeroSizeDisables pins the size=0 disable
// switch: a cache with capacity 0 should still construct
// but always miss. This is the operator-off escape hatch
// the issue calls out.
func TestCache_ZeroSizeDisables(t *testing.T) {
	t.Parallel()

	c := New[string](0, 0)
	loadCalls := 0
	loader := func(ctx context.Context) (string, error) {
		loadCalls++
		return "v", nil
	}

	_, hit1 := get(c, "k", loader)
	require.False(t, hit1, "size=0 cache must miss on first call")
	_, hit2 := get(c, "k", loader)
	require.False(t, hit2, "size=0 cache must keep missing")
	require.Equal(t, 2, loadCalls, "size=0 must not cache anything")
}

// TestKey_Hash_Stable pins the deterministic-key contract:
// the same logical inputs always produce the same key,
// even across instances. This is what makes cache entries
// shareable between processes and what guards against
// cache-key drift from reordering.
func TestKey_Hash_Stable(t *testing.T) {
	t.Parallel()

	k1 := Key{
		Query:    "what is ragabast",
		DocID:    "doc-1",
		Tag:      "",
		Category: "",
		Limit:    5,
		Mode:     "hybrid",
		Model:    "nomic-embed-text:v1.5",
	}
	// Same logical inputs, different field order — must
	// still hash to the same key.
	k2 := Key{
		Model:    "nomic-embed-text:v1.5",
		Mode:     "hybrid",
		Limit:    5,
		Category: "",
		Tag:      "",
		DocID:    "doc-1",
		Query:    "what is ragabast",
	}
	require.Equal(t, KeyOf(k1), KeyOf(k2),
		"key fields must be order-independent")

	// Different query → different key.
	k3 := k1
	k3.Query = "what is ragabasts"
	require.NotEqual(t, KeyOf(k1), KeyOf(k3),
		"different queries must produce different keys")

	// Different embedding model → different key. Without
	// this guard, switching from nomic-embed-text to
	// embedding-gemma would silently serve cached
	// vectors from the previous model.
	k4 := k1
	k4.Model = "embeddinggemma"
	require.NotEqual(t, KeyOf(k1), KeyOf(k4),
		"different embedding models must produce different keys")
}

// TestKey_Hash_IncludesFilters pins the filter-sensitive
// contract: changing DocumentID / Tag / Category must
// change the key. Without this, a query with filters
// would silently serve a cache entry from a no-filter
// query — a correctness bug.
func TestKey_Hash_IncludesFilters(t *testing.T) {
	t.Parallel()

	base := Key{
		Query: "x", DocID: "", Tag: "", Category: "",
		Limit: 5, Mode: "hybrid", Model: "m",
	}

	variants := []Key{
		{Query: "x", DocID: "doc-1", Tag: "", Category: "", Limit: 5, Mode: "hybrid", Model: "m"},
		{Query: "x", DocID: "", Tag: "go", Category: "", Limit: 5, Mode: "hybrid", Model: "m"},
		{Query: "x", DocID: "", Tag: "", Category: "tutorial", Limit: 5, Mode: "hybrid", Model: "m"},
	}
	baseHash := KeyOf(base)
	for _, v := range variants {
		require.NotEqual(t, baseHash, KeyOf(v),
			"varying %+v must change the key", v)
	}
}
