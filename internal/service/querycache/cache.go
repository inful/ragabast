// Package querycache implements a bounded LRU cache with
// optional TTL, designed for the ragabast query path:
//
//   - Service.Search and Service.HybridSearch consult the
//     cache before re-running the embedding + vector DB
//     pipeline.
//   - IngestDocument, DeleteDocument, and DeleteChunk
//     call Clear() because any new chunk could shift
//     result rankings.
//   - /api/health surfaces the hit-rate via Stats() so
//     operators can see the cache working.
//
// The cache key (Key, KeyOf) is a deterministic hash of
// (query, filters, limit, mode, embedding_model). Field
// order doesn't affect the hash so refactors stay safe;
// the embedding model is part of the key so swapping
// nomic-embed-text for embedding-gemma doesn't silently
// serve cached vectors from the previous model.
package querycache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Key is the set of inputs that distinguish a query
// result. Two searches with the same Key hash MUST
// produce identical results (modulo time, which is
// intentionally NOT in the key — search results are
// point-in-time, but operators don't expect re-running
// the same query to ever return different chunks).
type Key struct {
	Query    string
	DocID    string
	Tag      string
	Category string
	Limit    int
	Mode     string // "hybrid" / "semantic" / "keyword"
	Model    string // embedding model name (matters for cache correctness)
}

// KeyOf returns the deterministic cache key for k. Field
// order is irrelevant — the function concatenates fields
// in a fixed order regardless of how the caller populated
// the struct.
func KeyOf(k Key) string {
	h := sha256.New()
	// Fprintf to a hash.Hash never errors — the Write method
	// returns (int, error) where the int is bytes written
	// and the error is always nil for hash implementations.
	// Ignoring both is safe and the standard idiom.
	fmt.Fprintf(h, "q=%s\x00", k.Query)      //nolint:errcheck
	fmt.Fprintf(h, "doc=%s\x00", k.DocID)    //nolint:errcheck
	fmt.Fprintf(h, "tag=%s\x00", k.Tag)      //nolint:errcheck
	fmt.Fprintf(h, "cat=%s\x00", k.Category) //nolint:errcheck
	fmt.Fprintf(h, "limit=%d\x00", k.Limit)  //nolint:errcheck
	fmt.Fprintf(h, "mode=%s\x00", k.Mode)    //nolint:errcheck
	fmt.Fprintf(h, "model=%s\x00", k.Model)  //nolint:errcheck
	return hex.EncodeToString(h.Sum(nil))
}

// Stats summarizes cache behavior for /api/health.
//
// Hits + Misses == Lookups. Size is the current entry
// count (≤ capacity). Capacity is the configured max;
// it's exposed so operators can tell at a glance whether
// their cache is over- or under-sized for the workload.
type Stats struct {
	Hits     int64
	Misses   int64
	Lookups  int64
	Size     int
	Capacity int
}

// Cache is a generic, bounded LRU cache with optional TTL.
//
// Capacity bounds memory: when full, the least-recently-USED
// entry is evicted (Go's container/list moves touched
// entries to the front, so "LRU" here is "least-recently-
// used-or-inserted").
//
// TTL bounds staleness: when > 0, entries older than TTL
// are treated as misses on Get. Useful for operators who
// want safety even if Clear() is forgotten somewhere.
//
// A zero capacity means "cache disabled" — Get always
// misses and the loader always runs. This is the operator-
// off escape hatch.
type Cache[V any] struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	order    *list.List               // front = MRU, back = LRU
	entries  map[string]*list.Element // hashed key → element
	hits     int64
	misses   int64
}

type entry[V any] struct {
	key       string
	value     V
	expiresAt time.Time // zero = no expiry
}

// New constructs a Cache with the given capacity and TTL.
// capacity <= 0 disables the cache (every Get misses).
// ttl <= 0 disables TTL expiry (entries live until evicted).
func New[V any](capacity int, ttl time.Duration) *Cache[V] {
	return &Cache[V]{
		capacity: capacity,
		ttl:      ttl,
		order:    list.New(),
		entries:  make(map[string]*list.Element),
	}
}

// Get returns the cached value for key, calling loader on
// miss and storing the result. The boolean reports whether
// the cache served the call (true = hit, false = miss).
//
// Concurrent calls are safe: the loader may run more than
// once for the same key under high contention, but only
// one result wins (the loser is discarded). For ragabast's
// search path this is acceptable — the underlying search
// is idempotent.
func (c *Cache[V]) Get(ctx context.Context, key string, loader func(context.Context) (V, error)) (V, bool, error) {
	if c == nil || c.capacity <= 0 {
		v, err := loader(ctx)
		return v, false, err
	}

	// Fast path: cache hit. Look up, check TTL, bump LRU.
	c.mu.Lock()
	if elem, ok := c.entries[key]; ok {
		e := elem.Value.(*entry[V])
		if !c.expired(e) {
			c.order.MoveToFront(elem)
			c.hits++
			c.mu.Unlock()
			return e.value, true, nil
		}
		// Expired — drop it and fall through to the loader path.
		c.order.Remove(elem)
		delete(c.entries, key)
	}
	c.misses++
	c.mu.Unlock()

	v, err := loader(ctx)
	if err != nil {
		// Don't cache the zero value on error — callers
		// that retry after a transient failure shouldn't
		// be served a stale failure from a previous run.
		var zero V
		return zero, false, err
	}

	// Slow path: insert. Re-check capacity under lock so a
	// race doesn't push us over the configured bound.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capacity > 0 {
		if _, exists := c.entries[key]; !exists {
			c.insertLocked(key, v)
			c.evictIfFullLocked()
		}
	}

	return v, false, nil
}

// insertLocked adds a new entry under the existing lock.
// Caller must hold c.mu. Expiry is computed here so the
// TTL semantics stay consistent across cache hit and
// miss paths.
func (c *Cache[V]) insertLocked(key string, v V) {
	e := &entry[V]{key: key, value: v}
	if c.ttl > 0 {
		e.expiresAt = time.Now().Add(c.ttl)
	}
	elem := c.order.PushFront(e)
	c.entries[key] = elem
}

// evictIfFullLocked drops the LRU entry while the cache
// exceeds capacity. Caller must hold c.mu.
func (c *Cache[V]) evictIfFullLocked() {
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			return
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*entry[V]).key)
	}
}

// Clear removes every entry. Stats counters are NOT
// reset — operators usually want to compare the hit/miss
// ratio across the most recent "stable" period, so reset
// is a separate concern (currently no caller needs it).
func (c *Cache[V]) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order.Init()
	c.entries = make(map[string]*list.Element)
}

// Stats returns a snapshot of the cache's hit/miss/size
// counters. The caller can compute hit-rate as Hits/Lookups.
func (c *Cache[V]) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		Hits:     c.hits,
		Misses:   c.misses,
		Lookups:  c.hits + c.misses,
		Size:     c.order.Len(),
		Capacity: c.capacity,
	}
}

// expired reports whether an entry is past its TTL. Always
// false when TTL is unset (ttl <= 0).
func (c *Cache[V]) expired(e *entry[V]) bool {
	if c.ttl <= 0 {
		return false
	}
	return time.Now().After(e.expiresAt)
}
