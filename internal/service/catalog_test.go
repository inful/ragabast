package service

import (
	"sort"
	"strings"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestCollectUnique_DedupesAndSorts(t *testing.T) {
	docs := []models.DocumentInfo{
		{Tags: []string{"go", "API"}, Categories: []string{"Guides"}},
		{Tags: []string{"Go", "rag"}, Categories: []string{"Reference"}},
		{Tags: []string{"rag"}, Categories: []string{"Guides"}},
	}

	got := collectUnique(
		docs,
		func(d *models.DocumentInfo) []string { return d.Tags },
		func(s string) string { return strings.ToLower(strings.TrimSpace(s)) },
	)
	want := []string{"api", "go", "rag"}
	require.Equal(t, want, got)
}

func TestCollectUnique_DropsEmptyAfterNormalize(t *testing.T) {
	docs := []models.DocumentInfo{
		{Categories: []string{"  ", "Reference", "\t"}},
	}

	got := collectUnique(
		docs,
		func(d *models.DocumentInfo) []string { return d.Categories },
		strings.TrimSpace,
	)
	require.Equal(t, []string{"Reference"}, got)
}

func TestCollectUnique_EmptyDocsReturnsEmptySlice(t *testing.T) {
	got := collectUnique(
		nil,
		func(d *models.DocumentInfo) []string { return d.Tags },
		strings.ToLower,
	)
	require.NotNil(t, got, "callers can range over the result without a nil check")
	require.Empty(t, got)
}

// TestCollectUnique_SortedOutput is a regression guard: callers
// (GetNormalizedTags, GetNormalizedCategories) expose the result
// to the web UI, and the existing UI snapshot tests assume a
// stable alphabetical order.
func TestCollectUnique_SortedOutput(t *testing.T) {
	docs := []models.DocumentInfo{
		{Tags: []string{"z", "a", "m"}},
	}

	got := collectUnique(
		docs,
		func(d *models.DocumentInfo) []string { return d.Tags },
		strings.ToLower,
	)
	require.True(t, sort.StringsAreSorted(got), "result must be sorted: %v", got)
}
