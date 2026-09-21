package service

import (
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
)

// applyDateFilters post-filters the SearchResult slice
// against the date-range fields in SearchFilters. Returns
// a new slice (the input is left untouched) containing
// only the results that pass every active filter.
//
// Why a post-filter (not chromem-go's Where filter)?
// chromem-go's Where is a `map[string]string` and only
// supports equality. Range comparisons (\`>= X and <= Y\`)
// need a different code path, so the service layer reads
// the document-level dates already attached to each
// SearchResult (populated by VectorDB.Search from the
// chunk metadata) and drops out-of-range results here.
//
// A nil pointer on any date filter means "no constraint
// on this side" — the corresponding check is a no-op. A
// nil date on a SearchResult also means "no constraint
// applies" — we don't drop results that lack dates, since
// the absence is most likely an old ingest from before
// the date fields were tracked.
//
// Bounds are inclusive on both sides: CreatedAfter = X
// keeps results where CreatedAt >= X. Operators get the
// intuitive half-open interval [X, Y] when they combine
// CreatedAfter and CreatedBefore.
func applyDateFilters(results []models.SearchResult, f vector.SearchFilters) []models.SearchResult {
	out := results
	filtered := false

	if f.CreatedAfter != nil {
		threshold := *f.CreatedAfter
		out = filterInPlace(out, func(r models.SearchResult) bool {
			return r.DocumentCreatedAt == nil || !r.DocumentCreatedAt.Before(threshold)
		}, &filtered)
	}
	if f.CreatedBefore != nil {
		threshold := *f.CreatedBefore
		out = filterInPlace(out, func(r models.SearchResult) bool {
			return r.DocumentCreatedAt == nil || !r.DocumentCreatedAt.After(threshold)
		}, &filtered)
	}
	if f.UpdatedAfter != nil {
		threshold := *f.UpdatedAfter
		out = filterInPlace(out, func(r models.SearchResult) bool {
			return r.DocumentUpdatedAt == nil || !r.DocumentUpdatedAt.Before(threshold)
		}, &filtered)
	}
	if f.UpdatedBefore != nil {
		threshold := *f.UpdatedBefore
		out = filterInPlace(out, func(r models.SearchResult) bool {
			return r.DocumentUpdatedAt == nil || !r.DocumentUpdatedAt.After(threshold)
		}, &filtered)
	}

	if !filtered {
		// No filter was active — return the input unchanged
		// to avoid an unnecessary copy.
		return results
	}
	return out
}

// filterInPlace walks the slice keeping only elements for
// which keep returns true. When no element was actually
// dropped (filtered stays false), the input slice is
// returned unchanged — the caller uses the *bool to
// decide whether to allocate a new slice or pass the
// original back.
func filterInPlace(results []models.SearchResult, keep func(models.SearchResult) bool, filtered *bool) []models.SearchResult {
	out := results[:0]
	changed := false
	for i, r := range results {
		if keep(r) {
			out = append(out, r)
			continue
		}
		// We dropped at least one element. From this point
		// forward, copy into a fresh slice so we don't
		// mutate the caller's backing array.
		if !changed {
			out = make([]models.SearchResult, 0, len(results))
			out = append(out, results[:i]...)
			changed = true
		}
	}
	if changed {
		*filtered = true
	}
	return out
}
