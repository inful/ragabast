package web

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestExtractLinksFromResults_DedupesAcrossChunks(t *testing.T) {
	results := []models.SearchResult{
		{ChunkID: "c1", DocumentURLs: []string{"https://a.example", "https://b.example"}},
		{ChunkID: "c2", DocumentURLs: []string{"https://a.example", "https://c.example"}},
		{ChunkID: "c3", DocumentURLs: []string{"https://b.example", ""}},
	}

	got := extractLinksFromResults(results)

	want := []string{"https://a.example", "https://b.example", "https://c.example"}
	require.Equal(t, want, got, "first occurrence wins; empties dropped; order preserved by first appearance")
}

func TestExtractLinksFromResults_TrimsWhitespace(t *testing.T) {
	results := []models.SearchResult{
		{DocumentURLs: []string{"  https://x.example  ", "\t", "https://x.example"}},
	}

	got := extractLinksFromResults(results)

	require.Equal(t, []string{"https://x.example"}, got, "whitespace-only entries are dropped, not retained as-is")
}

func TestExtractLinksFromResults_EmptyInputReturnsEmptySlice(t *testing.T) {
	got := extractLinksFromResults(nil)
	require.NotNil(t, got, "callers may range without a nil check")
	require.Empty(t, got)
}

// TestExtractLinksFromResults_OrderIsFirstSeen guards the API
// contract: callers (link-suggestions) use this to truncate the
// list, so reordering would change which URLs the client sees.
func TestExtractLinksFromResults_OrderIsFirstSeen(t *testing.T) {
	results := []models.SearchResult{
		{DocumentURLs: []string{"https://third"}},
		{DocumentURLs: []string{"https://first"}},
		{DocumentURLs: []string{"https://second"}},
	}

	got := extractLinksFromResults(results)
	require.Equal(t,
		[]string{"https://third", "https://first", "https://second"},
		got,
		"first-seen order required",
	)
}
