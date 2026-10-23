package vector

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchIndex_DeleteDocumentRemovesEveryChunk pins audit
// finding 7: deleting a document must remove EVERY chunk that
// belongs to it, and it must do so in ONE index operation.
//
// The old implementation issued one bleve Delete per chunk, so a
// document with N chunks cost N index round-trips — each one a
// separate write to the on-disk index. A bulk delete of a
// 500-chunk document meant 500 writes where one batch would do.
//
// The behavior is identical either way, so the observable
// difference is the number of index writes. The countingIndex
// wrapper below records them.
func TestSearchIndex_DeleteDocumentRemovesEveryChunk(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	// Three chunks on doc-1, one on doc-2.
	require.NoError(t, idx.AddBatch([]chunkSummary{
		csMeta("c1", "doc-1", "alpha", []string{"t"}),
		csMeta("c2", "doc-1", "bravo", []string{"t"}),
		csMeta("c3", "doc-1", "charlie", []string{"t"}),
		csMeta("c4", "doc-2", "delta", []string{"t"}),
	}))

	// Count index operations from here on.
	counter := newCountingIndex(idx.index)
	idx.index = counter

	require.NoError(t, idx.DeleteDocument("doc-1"))

	// doc-1 is gone; doc-2 survives.
	res, err := idx.Search("alpha", 10, SearchFilters{})
	require.NoError(t, err)
	assert.Empty(t, res, "every chunk of the deleted document must be gone")

	size, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 1, size, "only doc-2's chunk should remain")

	assert.Zero(t, counter.deletes,
		"the deletes must go through one batch, not one index write per chunk")
	assert.Equal(t, 1, counter.batches,
		"the whole document must be deleted in a single index operation")
}

// TestSearchIndex_DeleteDocumentUnknownIsNoop pins that deleting
// a document that was never indexed is not an error. The bulk
// batch must not change that contract.
func TestSearchIndex_DeleteDocumentUnknownIsNoop(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csMeta("c1", "doc-1", "alpha", nil),
	}))

	require.NoError(t, idx.DeleteDocument("doc-never-indexed"))

	size, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 1, size, "an unknown document must not remove anything")
}
