package service

import (
	"github.com/ragabast/internal/service/querycache"
)

// QueryCacheStats is the snapshot of cache counters that
// /api/health/full surfaces to operators. Defined as a
// re-export of querycache.Stats so callers don't need to
// import the querycache package directly.
//
// The struct lives here (not in querycache) because the
// web layer needs a service-level type to put on
// serviceAPI; pulling querycache.Stats through service
// avoids leaking the cache implementation detail into
// every layer that surfaces the stats.
type QueryCacheStats = querycache.Stats

// QueryCacheStats returns a snapshot of the current cache
// state. Called by /api/health/full to surface hit-rate
// observability; safe to call concurrently.
func (s *Service) QueryCacheStats() QueryCacheStats {
	if s.cache == nil {
		return QueryCacheStats{}
	}
	return s.cache.Stats()
}
