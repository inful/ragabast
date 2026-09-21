package web

import (
	"testing"
	"time"

	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseSearchFilters_AllEmpty pins the no-op path:
// nil pointers and empty strings leave the filter
// untouched (the date fields stay nil).
func TestParseSearchFilters_AllEmpty(t *testing.T) {
	t.Parallel()

	got, err := parseSearchFilters(service.SearchFilters{
		DocumentID: "doc-1",
		Tag:        "go",
	}, nil, nil, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "doc-1", got.DocumentID)
	assert.Equal(t, "go", got.Tag)
	assert.Nil(t, got.CreatedAfter)
	assert.Nil(t, got.CreatedBefore)
	assert.Nil(t, got.UpdatedAfter)
	assert.Nil(t, got.UpdatedBefore)
}

// TestParseSearchFilters_AllSet verifies each RFC3339
// field round-trips into a *time.Time.
func TestParseSearchFilters_AllSet(t *testing.T) {
	t.Parallel()

	ca := "2024-01-01T00:00:00Z"
	cb := "2024-06-30T23:59:59Z"
	ua := "2024-02-15T12:00:00Z"
	ub := "2024-12-31T23:59:59Z"

	got, err := parseSearchFilters(service.SearchFilters{}, &ca, &cb, &ua, &ub)
	require.NoError(t, err)

	require.NotNil(t, got.CreatedAfter)
	assert.Equal(t, 2024, got.CreatedAfter.Year())
	assert.Equal(t, time.January, got.CreatedAfter.Month())
	require.NotNil(t, got.CreatedBefore)
	require.NotNil(t, got.UpdatedAfter)
	require.NotNil(t, got.UpdatedBefore)
}

// TestParseSearchFilters_EmptyStringTreatedAsAbsent
// pins the convention that an empty string field is
// equivalent to absence — operators sometimes send
// `{created_after: ""}` to "clear" a filter and that
// must work.
func TestParseSearchFilters_EmptyStringTreatedAsAbsent(t *testing.T) {
	t.Parallel()

	empty := ""
	got, err := parseSearchFilters(service.SearchFilters{}, &empty, nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, got.CreatedAfter, "empty string must NOT produce a nil-time filter")
}

// TestParseSearchFilters_InvalidRFC3339_ReturnsError
// pins the validation contract: a malformed date string
// returns an error naming the offending field so the
// caller can fix it. The handler maps this error to 400.
func TestParseSearchFilters_InvalidRFC3339_ReturnsError(t *testing.T) {
	t.Parallel()

	bogus := "not-a-date"
	_, err := parseSearchFilters(service.SearchFilters{}, nil, &bogus, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "created_before",
		"error must name the offending field")
}
