package service

import (
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
)

// applySourceKindFilters post-filters the SearchResult slice
// against the SourceKinds field in SearchFilters. Returns a new
// slice containing only results whose SourceKind is in the
// requested set; results with SourceKind == SourceUnknown are
// dropped from any non-empty filter.
//
// Why a post-filter (not chromem-go's Where)? See the
// SearchFilters docstring on SourceKinds. The post-filter reads
// each result's SourceKind (populated at read time via
// inferSourceKind in the vector layer) and drops anything that
// doesn't match. This mirrors the applyDateFilters pattern: an
// empty filter returns the input unchanged (same backing array,
// no copy).
//
//	Empty SourceKinds  → all results kept
//	SourceUnknown on  → kept only when filter is empty (the
//	  a result            "no preference" interpretation)
func applySourceKindFilters(results []models.SearchResult, f vector.SearchFilters) []models.SearchResult {
	if len(f.SourceKinds) == 0 {
		return results
	}
	wanted := make(map[models.SourceKind]struct{}, len(f.SourceKinds))
	for _, k := range f.SourceKinds {
		wanted[k] = struct{}{}
	}
	out := results
	filtered := false
	out = filterInPlace(out, func(r models.SearchResult) bool {
		_, ok := wanted[r.SourceKind]
		return ok
	}, &filtered)
	if !filtered {
		return results
	}
	return out
}
