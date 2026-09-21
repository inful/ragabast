package vector

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureVO builds a VectorOperations backed by an in-memory
// vector DB plus a fresh bleve search index in a temp dir.
// Returns the VectorOperations, both stores for direct
// inspection, and a cleanup func.
func fixtureVO(t *testing.T) (*VectorOperations, *VectorDB, *SearchIndex) {
	t.Helper()
	db, err := NewVectorDB("test-"+t.Name(), 4, "", "test-model")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "search.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	// We pass nil for embeddings: every test that calls
	// IngestDocument needs real embeddings, so we wire a
	// stub via setEmbeddings. Tests that don't ingest
	// ignore the field.
	vo := NewVectorOperations(db, nil)
	vo.SetSearchIndex(idx)
	return vo, db, idx
}

// IngestDocument writes every chunk into both the vector DB
// AND the bleve search index. Verified by counting documents
// in both stores after a single ingest.
func TestVectorOperations_IngestDocument_PopulatesBothStores(t *testing.T) {
	t.Parallel()

	vo, db, idx := fixtureVO(t)
	// Stub embeddings: identity-mapped into 4-D unit vectors.
	// The vector DB dimension is 4; we set the chunk's
	// "embedding" directly via a fake generator.
	vo.embeddings = stubEmbeddings(t)

	doc := testDoc(t, "doc-1", "alpha bravo charlie", "kubernetes ingress tls")
	require.NoError(t, vo.IngestDocument(context.Background(), doc))

	// Vector DB has the chunk.
	dbCount, err := db.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, dbCount, "vector DB should hold 1 chunk")

	// Search index has the chunk.
	idxCount, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 1, idxCount, "search index should hold 1 chunk")
}

// IngestDocument with a duplicate document (same fingerprint)
// is a no-op for BOTH stores — neither gets a stale entry.
func TestVectorOperations_IngestDocument_DuplicateFingerprintIsNoop(t *testing.T) {
	t.Parallel()

	vo, db, idx := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	doc := testDoc(t, "doc-1", "alpha", "kubernetes ingress tls")
	require.NoError(t, vo.IngestDocument(context.Background(), doc))
	require.NoError(t, vo.IngestDocument(context.Background(), doc))

	dbCount, err := db.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, dbCount)

	idxCount, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 1, idxCount)
}

// Re-ingest with a NEW fingerprint clears the old entries
// from both stores before writing the new ones. Prevents
// stale chunks from both the vector DB and the search index.
func TestVectorOperations_IngestDocument_FingerprintChangeClearsBothStores(t *testing.T) {
	t.Parallel()

	vo, db, idx := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	first := testDocWithFingerprint(t, "doc-1", "alpha", "first", "kubernetes")
	require.NoError(t, vo.IngestDocument(context.Background(), first))

	second := testDocWithFingerprint(t, "doc-1", "alpha", "second", "kubernetes updated")
	require.NoError(t, vo.IngestDocument(context.Background(), second))

	// Vector DB still has 1 chunk (the new one).
	dbCount, err := db.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, dbCount)

	// Search index also has 1 chunk (the new one).
	idxCount, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 1, idxCount)

	// And keyword search for "kubernetes" finds the new
	// content ("updated") — proves the old entry is gone.
	hits, err := idx.Search("kubernetes", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	// SearchResult carries the stored content (well, bleve
	// doesn't return content in our SearchHit struct, but
	// we can confirm via search-by-keyword of the new term).
	updatedHits, err := idx.Search("updated", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, updatedHits, 1, "new chunk content must be searchable")
}

// DeleteDocument clears the document's chunks from both
// stores in one call.
func TestVectorOperations_DeleteDocument_ClearsBothStores(t *testing.T) {
	t.Parallel()

	vo, db, idx := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	doc := testDoc(t, "doc-1", "alpha", "kubernetes ingress tls")
	require.NoError(t, vo.IngestDocument(context.Background(), doc))

	require.NoError(t, vo.DeleteDocument(context.Background(), "doc-1"))

	dbCount, err := db.Count()
	require.NoError(t, err)
	assert.Equal(t, 0, dbCount)

	idxCount, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 0, idxCount)
}

// DeleteChunk clears a single chunk from both stores.
func TestVectorOperations_DeleteChunk_ClearsBothStores(t *testing.T) {
	t.Parallel()

	vo, db, idx := fixtureVO(t)
	vo.embeddings = stubEmbeddings(t)

	// Build a doc with two chunks so we can delete one.
	doc := testDocTwoChunks(t, "doc-1", "first content", "second content")
	require.NoError(t, vo.IngestDocument(context.Background(), doc))

	firstID := doc.Chunks[0].ID
	require.NoError(t, vo.DeleteChunk(context.Background(), firstID))

	dbCount, err := db.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, dbCount, "one chunk remains in vector DB")

	idxCount, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 1, idxCount, "one chunk remains in search index")

	// The remaining chunk is the one we did NOT delete.
	hits, err := idx.Search("second", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, doc.Chunks[1].ID, hits[0].ID)
}

// IngestDocument without a SearchIndex wired is a no-op for
// the keyword store — graceful degradation. Useful for
// single-store tests that don't want a bleve dependency.
func TestVectorOperations_IngestDocument_NoSearchIndexIsGraceful(t *testing.T) {
	t.Parallel()

	db, err := NewVectorDB("test-"+t.Name(), 4, "", "test-model")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	vo := NewVectorOperations(db, stubEmbeddings(t))
	// No SetSearchIndex call — the field is nil.
	doc := testDoc(t, "doc-1", "alpha", "kubernetes")
	require.NoError(t, vo.IngestDocument(context.Background(), doc))

	dbCount, err := db.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, dbCount)
}
