package vector

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase-6 adversarial sweep. These tests cover failure modes
// the green-path tests in search_index_test.go and
// hybrid_test.go don't exercise:
//
//  - filter applied to BOTH rankings in hybrid mode (precedence)
//  - tag filter matching one tag among many on a chunk
//  - hybrid with a keyword-only chunk (no vector embedding)
//  - search-after-delete returns no stale hits
//  - re-ingest with concurrent writers leaves exactly one chunk
//  - ingest + search concurrent under -race
//  - persistence: closing the bleve index and reopening via
//    LoadSearchIndex recovers the keyword index state

// Filter precedence: a chunk that fails the filter must NOT
// appear in hybrid results, even if it ranks highly on
// raw relevance. Filters win over relevance.
func TestPhase6_Hybrid_FilterPrecedenceOverRelevance(t *testing.T) {
	t.Parallel()

	vo, _, _ := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	// Two docs that both contain "kubernetes"; only one
	// matches the tag filter.
	require.NoError(t, vo.IngestDocument(context.Background(),
		testDocMetaTags(t, "doc-a", "alpha", "kubernetes", []string{"security"})))
	require.NoError(t, vo.IngestDocument(context.Background(),
		testDocMetaTags(t, "doc-b", "bravo", "kubernetes", []string{"networking"})))

	results, err := vo.SearchHybrid(context.Background(),
		"kubernetes", 5,
		SearchFilters{Tag: "security"}, ModeHybrid)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	for _, r := range results {
		assert.Equal(t, "doc-a", r.DocumentID,
			"hybrid results must respect tag filter; doc-b should not appear")
	}
}

// Tag filter matches one of many tags on a chunk. bleve's
// MatchQuery tokenizes both the field value and the query,
// and returns the chunk if any token matches.
func TestPhase6_KeywordFilter_MatchesAnyTag(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		{
			ID:         "c1",
			Content:    "kubernetes ingress tls handshake",
			DocumentID: "doc-a",
			Tags:       []string{"go", "api", "security"},
		},
	}))

	hits, err := idx.Search("kubernetes", 5, SearchFilters{Tag: "security"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "c1", hits[0].ID)
}

// Hybrid finds a chunk that exists ONLY in the keyword
// index. The enrichment path surfaces it via the
// keyword-index-only fallback.
func TestPhase6_Hybrid_KeywordOnlyChunk(t *testing.T) {
	t.Parallel()

	vo, _, idx := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	require.NoError(t, vo.IngestDocument(context.Background(),
		testDoc(t, "doc-a", "alpha", "kubernetes")))

	require.NoError(t, idx.AddBatch([]chunkSummary{
		{ID: "kw-only", Content: "kubernetes ghost entry", DocumentID: "ghost-doc"},
	}))

	results, err := vo.SearchHybrid(context.Background(),
		"kubernetes", 10, SearchFilters{}, ModeHybrid)
	require.NoError(t, err)
	ids := rrfResultIDs(results)
	assert.Contains(t, ids, "kw-only",
		"hybrid must surface keyword-only chunks")
}

// Search-after-delete returns no stale hits in either store.
func TestPhase6_SearchAfterDeleteIsEmpty(t *testing.T) {
	t.Parallel()

	vo, _, idx := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	require.NoError(t, vo.IngestDocument(context.Background(),
		testDoc(t, "doc-a", "alpha", "kubernetes ingress tls")))

	hybrid, err := vo.SearchHybrid(context.Background(), "kubernetes", 5,
		SearchFilters{}, ModeHybrid)
	require.NoError(t, err)
	require.NotEmpty(t, hybrid)

	require.NoError(t, vo.DeleteDocument(context.Background(), "doc-a"))

	dbCount, err := vo.db.Count()
	require.NoError(t, err)
	assert.Equal(t, 0, dbCount)

	idxCount, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 0, idxCount)

	after, err := vo.SearchHybrid(context.Background(), "kubernetes", 5,
		SearchFilters{}, ModeHybrid)
	require.NoError(t, err)
	assert.Empty(t, after, "no chunks after delete → no hits in hybrid")
}

// Re-ingest under concurrent writers must not double-count
// chunks. With deterministic chunk IDs the bleve AddBatch
// should overwrite rather than append.
func TestPhase6_Reingest_ConcurrentWritersSingleChunk(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	doc := []chunkSummary{
		{ID: "c1", Content: "alpha bravo charlie delta echo"},
	}

	const writers = 8
	var wg sync.WaitGroup
	wg.Add(writers)
	for range writers {
		go func() {
			defer wg.Done()
			assert.NoError(t, idx.AddBatch(doc))
		}()
	}
	wg.Wait()

	size, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 1, size,
		"concurrent AddBatch of the same chunk ID must dedupe to one entry")

	hits, err := idx.Search("alpha", 10, SearchFilters{})
	require.NoError(t, err)
	assert.Len(t, hits, 1)
}

// Hybrid ingest + concurrent search under -race. No crashes,
// no index corruption, results still bounded by limit.
func TestPhase6_Concurrent_IngestAndSearch(t *testing.T) {
	t.Parallel()

	vo, _, _ := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	for i := range 4 {
		id := "seed-" + string(rune('a'+i))
		require.NoError(t, vo.IngestDocument(context.Background(),
			testDoc(t, id, "alpha", "kubernetes networking "+string(rune('a'+i)))))
	}

	const goroutines = 6
	const ops = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(g int) {
			defer wg.Done()
			for i := range ops {
				if g%2 == 0 {
					id := "g" + string(rune('a'+g)) + "-i" + string(rune('a'+i%5))
					_ = vo.IngestDocument(context.Background(),
						testDoc(t, id, "alpha", "kubernetes shard "+id))
				} else {
					_, _ = vo.SearchHybrid(context.Background(),
						"kubernetes", 5, SearchFilters{}, ModeHybrid)
				}
			}
		}(g)
	}
	wg.Wait()

	results, err := vo.SearchHybrid(context.Background(),
		"kubernetes", 5, SearchFilters{}, ModeHybrid)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 5,
		"limit must still cap results after concurrent activity")
}

// Persistence: closing the bleve index and reopening via
// LoadSearchIndex recovers the keyword index state.
func TestPhase6_Persistence_KeywordSurvivesReopen(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "search.bleve")

	idx, err := NewSearchIndex(dir)
	require.NoError(t, err)
	require.NoError(t, idx.AddBatch([]chunkSummary{
		{ID: "a", Content: "kubernetes ingress"},
		{ID: "b", Content: "kubernetes networking"},
		{ID: "c", Content: "database backup"},
	}))
	require.NoError(t, idx.Close())

	idx2, err := LoadSearchIndex(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx2.Close() })

	hits, err := idx2.Search("kubernetes", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 2)
}

// testDocMetaTags is like testDoc but sets the chunk's
// DocumentTags to the supplied list. Used by the filter
// precedence tests, where we need precise tag control.
func testDocMetaTags(t *testing.T, id, fp, chunkContent string, tags []string) *models.Document {
	t.Helper()
	doc := testDoc(t, id, fp, chunkContent)
	doc.Chunks[0].DocumentTags = tags
	return doc
}
