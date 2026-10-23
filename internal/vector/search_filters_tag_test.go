package vector

import (
	"context"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestSearchFilters_SemanticMode_TagFilterKeepsMultiTagDocuments pins
// that a tag filter does not silently drop every document that carries
// more than one tag.
//
// The tag list is stored as one newline-joined metadata value
// ("go\napi"). chromem-go's Where filter is exact equality on that
// value, so pushing the filter into chromem matched only documents
// whose tag list was exactly {"go"} — every multi-tag document
// vanished from the result while the endpoint still answered 200.
func TestSearchFilters_SemanticMode_TagFilterKeepsMultiTagDocuments(t *testing.T) {
	vo, _, _ := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	ctx := context.Background()
	require.NoError(t, vo.IngestDocument(ctx,
		testDocMetaTags(t, "doc-multi", "alpha", "kubernetes ingress tls", []string{"go", "api"})))
	require.NoError(t, vo.IngestDocument(ctx,
		testDocMetaTags(t, "doc-single", "bravo", "kubernetes ingress tls", []string{"go"})))
	require.NoError(t, vo.IngestDocument(ctx,
		testDocMetaTags(t, "doc-other", "charlie", "kubernetes ingress tls", []string{"rust"})))

	results, err := vo.SearchHybrid(ctx, "kubernetes", 10,
		SearchFilters{Tag: "go"}, ModeSemantic)
	require.NoError(t, err)

	ids := resultDocIDs(results)
	require.Contains(t, ids, "doc-multi",
		"a document with several tags must survive a tag filter")
	require.Contains(t, ids, "doc-single")
	require.NotContains(t, ids, "doc-other",
		"a document without the tag must not survive the filter")
}

// TestSearchFilters_CategoryFilterKeepsMultiCategoryDocuments is the
// category twin of the tag test — categories are stored the same way.
func TestSearchFilters_CategoryFilterKeepsMultiCategoryDocuments(t *testing.T) {
	vo, _, _ := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	ctx := context.Background()
	multi := testDocMetaTags(t, "doc-multi", "alpha", "kubernetes ingress tls", []string{"go"})
	multi.Chunks[0].DocumentCategories = []string{"c1", "c2"}
	require.NoError(t, vo.IngestDocument(ctx, multi))

	results, err := vo.SearchHybrid(ctx, "kubernetes", 10,
		SearchFilters{Category: "c2"}, ModeSemantic)
	require.NoError(t, err)
	require.Contains(t, resultDocIDs(results), "doc-multi",
		"a document with several categories must survive a category filter")
}

// TestSearchFilters_TagFilterKeepsMultiWordTags pins that a tag with
// a space in it still matches.
//
// The filter value used to be stripped of EVERY space (not just the
// surrounding ones), so "machine learning" became "machinelearning"
// and matched nothing on either side of the fusion.
func TestSearchFilters_TagFilterKeepsMultiWordTags(t *testing.T) {
	vo, _, _ := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	ctx := context.Background()
	require.NoError(t, vo.IngestDocument(ctx,
		testDocMetaTags(t, "doc-ml", "alpha", "kubernetes ingress tls", []string{"machine learning"})))

	results, err := vo.SearchHybrid(ctx, "kubernetes", 10,
		SearchFilters{Tag: "machine learning"}, ModeSemantic)
	require.NoError(t, err)
	require.Contains(t, resultDocIDs(results), "doc-ml",
		"a multi-word tag must match the document that carries it")
}

func resultDocIDs(results []models.SearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.DocumentID)
	}
	return out
}
