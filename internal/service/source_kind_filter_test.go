package service

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/require"
)

// TestApplySourceKindFilters pins the post-filter contract that
// mirrors applyDateFilters. The filter is empty (= no constraint)
// by default, so a nil/empty SourceKinds on the filters struct
// returns the input untouched. A non-empty filter keeps only
// results whose SourceKind appears in the filter set.
//
// The post-filter reads SearchResult.SourceKind, which is
// populated at the vector layer (with UID-prefix inference for
// legacy chunks) — see inferSourceKind in internal/vector.
func TestApplySourceKindFilters(t *testing.T) {
	results := []models.SearchResult{
		{ChunkID: "a", SourceKind: models.SourceDocbuilder},
		{ChunkID: "b", SourceKind: models.SourceGitLab},
		{ChunkID: "c", SourceKind: models.SourceDocbuilder},
		{ChunkID: "d", SourceKind: models.SourceUnknown}, // legacy chunk with no kind inferable
	}

	cases := []struct {
		name    string
		filters vector.SearchFilters
		wantIDs []string
	}{
		{
			name:    "empty filter is a no-op (returns input unchanged)",
			filters: vector.SearchFilters{},
			wantIDs: []string{"a", "b", "c", "d"},
		},
		{
			name:    "nil SourceKinds is a no-op",
			filters: vector.SearchFilters{SourceKinds: nil},
			wantIDs: []string{"a", "b", "c", "d"},
		},
		{
			name:    "single-kind filter keeps matching only",
			filters: vector.SearchFilters{SourceKinds: []models.SourceKind{models.SourceGitLab}},
			wantIDs: []string{"b"},
		},
		{
			name:    "multi-kind filter keeps union of matching",
			filters: vector.SearchFilters{SourceKinds: []models.SourceKind{models.SourceGitLab, models.SourceDocbuilder}},
			wantIDs: []string{"a", "b", "c"},
		},
		{
			name:    "SourceUnknown is not in any non-empty filter by default",
			filters: vector.SearchFilters{SourceKinds: []models.SourceKind{models.SourceDocbuilder}},
			wantIDs: []string{"a", "c"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applySourceKindFilters(results, tc.filters)
			gotIDs := make([]string, 0, len(got))
			for _, r := range got {
				gotIDs = append(gotIDs, r.ChunkID)
			}
			require.Equal(t, tc.wantIDs, gotIDs)
		})
	}
}

// TestApplySourceKindFilters_NoOpReturnsSameBackingArray verifies
// the optimization that an empty filter returns the input slice
// unchanged (same backing array, not a copy). This matters for
// callers that pass the slice further down without defensive
// copies; the post-filter is supposed to be cheap.
func TestApplySourceKindFilters_NoOpReturnsSameBackingArray(t *testing.T) {
	results := []models.SearchResult{{ChunkID: "a"}}
	got := applySourceKindFilters(results, vector.SearchFilters{})
	require.Same(t, &results[0], &got[0],
		"empty filter should return the input slice unchanged (same backing array)")
}
