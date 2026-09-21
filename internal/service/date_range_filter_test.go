package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyDateFilters_CreatedAfter pins the basic
// contract of the post-filter applied after the vector
// DB returns results. DocumentCreatedAt older than the
// threshold must be excluded; newer must be kept.
func TestApplyDateFilters_CreatedAfter(t *testing.T) {
	t.Parallel()

	threshold := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []models.SearchResult{
		{DocumentID: "old", DocumentCreatedAt: timePtr(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))},
		{DocumentID: "recent", DocumentCreatedAt: timePtr(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))},
		{DocumentID: "exact", DocumentCreatedAt: timePtr(t, threshold)},
	}

	out := applyDateFilters(results, vector.SearchFilters{CreatedAfter: &threshold})

	ids := docIDs(out)
	assert.ElementsMatch(t, []string{"recent", "exact"}, ids,
		"CreatedAfter must keep results at-or-after the threshold (inclusive)")
}

// TestApplyDateFilters_CreatedBefore pins the upper-bound
// half: results whose CreatedAt is later than the cutoff
// are excluded.
func TestApplyDateFilters_CreatedBefore(t *testing.T) {
	t.Parallel()

	cutoff := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	results := []models.SearchResult{
		{DocumentID: "old", DocumentCreatedAt: timePtr(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))},
		{DocumentID: "recent", DocumentCreatedAt: timePtr(t, time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC))},
		{DocumentID: "exact", DocumentCreatedAt: timePtr(t, cutoff)},
	}

	out := applyDateFilters(results, vector.SearchFilters{CreatedBefore: &cutoff})

	ids := docIDs(out)
	assert.ElementsMatch(t, []string{"old", "exact"}, ids,
		"CreatedBefore must keep results at-or-before the cutoff (inclusive)")
}

// TestApplyDateFilters_UpdatedAfter pins the UpdatedAfter
// half, same shape as CreatedAfter but on the UpdatedAt
// field.
func TestApplyDateFilters_UpdatedAfter(t *testing.T) {
	t.Parallel()

	threshold := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []models.SearchResult{
		{DocumentID: "stale", DocumentUpdatedAt: timePtr(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))},
		{DocumentID: "fresh", DocumentUpdatedAt: timePtr(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))},
	}

	out := applyDateFilters(results, vector.SearchFilters{UpdatedAfter: &threshold})

	ids := docIDs(out)
	assert.ElementsMatch(t, []string{"fresh"}, ids)
}

// TestApplyDateFilters_UpdatedBefore pins the UpdatedBefore
// half — same shape as CreatedBefore.
func TestApplyDateFilters_UpdatedBefore(t *testing.T) {
	t.Parallel()

	cutoff := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	results := []models.SearchResult{
		{DocumentID: "stale", DocumentUpdatedAt: timePtr(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))},
		{DocumentID: "fresh", DocumentUpdatedAt: timePtr(t, time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC))},
	}

	out := applyDateFilters(results, vector.SearchFilters{UpdatedBefore: &cutoff})

	ids := docIDs(out)
	assert.ElementsMatch(t, []string{"stale"}, ids)
}

// TestApplyDateFilters_AllFourCombined pins the intersection:
// CreatedAfter, CreatedBefore, UpdatedAfter, UpdatedBefore
// all active simultaneously. Only results that pass ALL
// four survive.
func TestApplyDateFilters_AllFourCombined(t *testing.T) {
	t.Parallel()

	createdAfter := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	createdBefore := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	updatedAfter := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedBefore := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	results := []models.SearchResult{
		// Inside every range — must survive.
		{DocumentID: "inside", DocumentCreatedAt: timePtr(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)), DocumentUpdatedAt: timePtr(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))},
		// Outside CreatedAfter.
		{DocumentID: "before-created", DocumentCreatedAt: timePtr(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)), DocumentUpdatedAt: timePtr(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))},
		// Outside CreatedBefore.
		{DocumentID: "after-created", DocumentCreatedAt: timePtr(t, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)), DocumentUpdatedAt: timePtr(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))},
		// Outside UpdatedAfter.
		{DocumentID: "before-updated", DocumentCreatedAt: timePtr(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)), DocumentUpdatedAt: timePtr(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))},
		// Outside UpdatedBefore.
		{DocumentID: "after-updated", DocumentCreatedAt: timePtr(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)), DocumentUpdatedAt: timePtr(t, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))},
	}

	out := applyDateFilters(results, vector.SearchFilters{
		CreatedAfter:  &createdAfter,
		CreatedBefore: &createdBefore,
		UpdatedAfter:  &updatedAfter,
		UpdatedBefore: &updatedBefore,
	})

	ids := docIDs(out)
	assert.Equal(t, []string{"inside"}, ids,
		"only the doc inside every range must survive (got %v)", ids)
}

// TestApplyDateFilters_NoFilters_AllPass pins the
// zero-pointer safety: when no date filters are set,
// every result must pass. This guards against an
// off-by-one bug where an uninitialized filter accidentally
// excludes everything.
func TestApplyDateFilters_NoFilters_AllPass(t *testing.T) {
	t.Parallel()

	results := []models.SearchResult{
		{DocumentID: "a"},
		{DocumentID: "b", DocumentCreatedAt: timePtr(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))},
		{DocumentID: "c", DocumentUpdatedAt: timePtr(t, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))},
	}

	out := applyDateFilters(results, vector.SearchFilters{})

	assert.Len(t, out, 3,
		"zero-value filters must NOT exclude anything")
}

// TestApplyDateFilters_NilPointer_AllPass pins the
// nil-pointer safety: a *time.Time that was never set
// (e.g. unmarshalled from a request that didn't include
// the field) must not panic and must not exclude any
// result.
func TestApplyDateFilters_NilPointer_AllPass(t *testing.T) {
	t.Parallel()

	results := []models.SearchResult{
		{DocumentID: "a"},
		{DocumentID: "b"},
	}

	out := applyDateFilters(results, vector.SearchFilters{
		CreatedAfter:  nil,
		CreatedBefore: nil,
		UpdatedAfter:  nil,
		UpdatedBefore: nil,
	})

	assert.Len(t, out, 2)
}

// TestApplyDateFilters_NilDatesOnResult_AllPass pins the
// opposite direction: a SearchResult whose dates are nil
// (e.g. ingested before the dates field existed, or
// chromem-go didn't return them) must NOT be excluded by
// the filter. The filter applies only when BOTH the
// filter and the result have a date.
func TestApplyDateFilters_NilDatesOnResult_AllPass(t *testing.T) {
	t.Parallel()

	threshold := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	results := []models.SearchResult{
		{DocumentID: "no-dates"},
	}

	out := applyDateFilters(results, vector.SearchFilters{
		CreatedAfter: &threshold,
	})

	assert.Len(t, out, 1,
		"results without dates must survive any date filter (incomplete data, not a violation)")
}

// TestSearchResult_DateFieldsJSON verifies the wire
// format: DocumentCreatedAt and DocumentUpdatedAt
// serialize as RFC3339 strings, and nil fields are
// omitted (backward compat for clients that don't know
// about the new fields).
func TestSearchResult_DateFieldsJSON(t *testing.T) {
	t.Parallel()

	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	r := models.SearchResult{
		ChunkID:           "c1",
		DocumentID:        "d1",
		DocumentCreatedAt: &now,
		DocumentUpdatedAt: &now,
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)
	s := string(data)
	require.Contains(t, s, `"document_created_at":"2024-06-15T12:00:00Z"`)
	require.Contains(t, s, `"document_updated_at":"2024-06-15T12:00:00Z"`)

	r2 := models.SearchResult{ChunkID: "c1", DocumentID: "d1"}
	data2, err := json.Marshal(r2)
	require.NoError(t, err)
	s2 := string(data2)
	require.False(t, strings.Contains(s2, "document_created_at"),
		"nil DocumentCreatedAt must be omitted from JSON")
	require.False(t, strings.Contains(s2, "document_updated_at"),
		"nil DocumentUpdatedAt must be omitted from JSON")
}

// --- helpers ---

func timePtr(t *testing.T, v time.Time) *time.Time {
	t.Helper()
	return &v
}

func docIDs(results []models.SearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.DocumentID
	}
	return out
}
