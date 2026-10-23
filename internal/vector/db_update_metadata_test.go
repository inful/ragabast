package vector

import (
	"context"
	"strconv"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestVectorDB_UpdateDocumentMetadata_AppliesPatchToRealStore drives
// the bulk-update path against the REAL vector store.
//
// The web-layer test for POST /api/documents/bulk-update stubs the
// service through the serviceAPI interface, so nothing below this
// line was ever executed. That is how the endpoint shipped broken:
// chromem's Delete takes (where, whereDocument, ids...) and the
// document filter was passed in the whereDocument slot, so every
// patch came back "unsupported whereDocument operator".
func TestVectorDB_UpdateDocumentMetadata_AppliesPatchToRealStore(t *testing.T) {
	db, err := NewVectorDB("test", 3, "", "test-model")
	require.NoError(t, err)

	ctx := context.Background()
	chunk := &models.Chunk{
		ID:                 "chunk-1",
		DocumentID:         "doc-1",
		DocumentTitle:      "Doc Title",
		Content:            "hello",
		HeaderPath:         "Doc Title",
		Level:              2,
		StartLine:          10,
		EndLine:            20,
		UID:                "u-1",
		Fingerprint:        "fp-1",
		DocumentTags:       []string{"t1"},
		DocumentCategories: []string{"c1"},
		SourceKind:         models.SourceGitLab,
	}
	require.NoError(t, db.AddChunksBatch(ctx, []*models.Chunk{chunk}, [][]float32{{1, 0, 0}}))

	err = db.UpdateDocumentMetadata(ctx, DocumentMetadataPatch{
		DocumentID: "doc-1",
		Tags:       []string{"t2"},
		MergeTags:  true,
		Categories: []string{"c2"},
	})
	require.NoError(t, err)

	got, err := db.GetChunk(ctx, "chunk-1")
	require.NoError(t, err, "the chunk must still be in the store after the metadata update")
	require.Equal(t, []string{"t1", "t2"}, got.DocumentTags, "merge mode keeps the existing tags")
	require.Equal(t, []string{"c2"}, got.DocumentCategories, "categories are replaced wholesale")
	require.Equal(t, 2, got.Level, "the metadata update must not reset the chunk level")
	require.Equal(t, 10, got.StartLine)
	require.Equal(t, 20, got.EndLine)
	require.Equal(t, models.SourceGitLab, got.SourceKind,
		"the metadata update must not lose the chunk's source kind")
	require.Equal(t, "hello", got.Content)
}

// TestVectorDB_UpdateDocumentMetadata_ReplacesTags pins the
// replace mode (mode=replace on the bulk-update endpoint).
func TestVectorDB_UpdateDocumentMetadata_ReplacesTags(t *testing.T) {
	db, err := NewVectorDB("test", 3, "", "test-model")
	require.NoError(t, err)

	ctx := context.Background()
	chunk := &models.Chunk{
		ID:           "chunk-1",
		DocumentID:   "doc-1",
		Content:      "hello",
		DocumentTags: []string{"t1", "t2"},
	}
	require.NoError(t, db.AddChunksBatch(ctx, []*models.Chunk{chunk}, [][]float32{{1, 0, 0}}))

	err = db.UpdateDocumentMetadata(ctx, DocumentMetadataPatch{
		DocumentID: "doc-1",
		Tags:       []string{"t3"},
	})
	require.NoError(t, err)

	got, err := db.GetChunk(ctx, "chunk-1")
	require.NoError(t, err)
	require.Equal(t, []string{"t3"}, got.DocumentTags)
}

// TestVectorDB_GetChunksByDocument_ReturnsEveryChunk pins that the
// per-document lookup is not capped at 1000 chunks. A document with
// more than 1000 chunks used to be reported with a truncated chunk
// list, and DeleteDocument only deleted the first 1000 — the rest
// stayed in the store as orphans that still matched searches.
func TestVectorDB_GetChunksByDocument_ReturnsEveryChunk(t *testing.T) {
	db, err := NewVectorDB("test", 3, "", "test-model")
	require.NoError(t, err)

	ctx := context.Background()
	const total = 1200
	chunks := make([]*models.Chunk, 0, total)
	embeddings := make([][]float32, 0, total)
	for i := 0; i < total; i++ {
		chunks = append(chunks, &models.Chunk{
			ID:         "chunk-" + strconv.Itoa(i),
			DocumentID: "doc-big",
			Content:    "body",
		})
		embeddings = append(embeddings, []float32{1, 0, 0})
	}
	require.NoError(t, db.AddChunksBatch(ctx, chunks, embeddings))

	got, err := db.GetChunksByDocument(ctx, "doc-big")
	require.NoError(t, err)
	require.Len(t, got, total, "every chunk of the document must be returned")

	require.NoError(t, db.DeleteDocument(ctx, "doc-big"))
	remaining, err := db.GetChunksByDocument(ctx, "doc-big")
	require.NoError(t, err)
	require.Empty(t, remaining, "every chunk of the document must be deleted")
}
