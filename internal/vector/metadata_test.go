package vector

import (
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestChunkToMetadata_AllOptionalFieldsPopulated pins the full
// shape of the chunk→metadata conversion. Any future field on
// models.Chunk that should land in metadata has to add a case
// here, so a regression shows up at test time.
func TestChunkToMetadata_AllOptionalFieldsPopulated(t *testing.T) {
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2024, 1, 2, 4, 5, 6, 0, time.UTC)
	chunk := &models.Chunk{
		ID:                 "chunk-1",
		DocumentID:         "doc-1",
		HeaderPath:         "Setup > Install",
		Level:              2,
		StartLine:          10,
		EndLine:            25,
		DocumentTitle:      "Doc Title",
		DocumentURLs:       []string{"https://a.example", "https://b.example"},
		DocumentTags:       []string{"go", "rag"},
		DocumentCategories: []string{"Guides"},
		DocumentCreatedAt:  created,
		DocumentUpdatedAt:  updated,
		Fingerprint:        "fp-1",
		UID:                "uid-1",
		ParentID:           "parent-1",
	}

	meta := chunkToMetadata(chunk.ID, chunk)

	require.Equal(t, "doc-1", meta["document_id"])
	require.Equal(t, "chunk-1", meta["chunk_id"])
	require.Equal(t, "Setup > Install", meta["header_path"])
	require.Equal(t, "2", meta["level"])
	require.Equal(t, "10", meta["start_line"])
	require.Equal(t, "25", meta["end_line"])
	require.Equal(t, "Doc Title", meta["document_title"])
	require.Equal(t, "https://a.example\nhttps://b.example", meta["document_urls"])
	require.Equal(t, "go\nrag", meta["document_tags"])
	require.Equal(t, "Guides", meta["document_categories"])
	require.Equal(t, "2024-01-02T03:04:05Z", meta["document_created_at"])
	require.Equal(t, "2024-01-02T04:05:06Z", meta["document_updated_at"])
	require.Equal(t, "fp-1", meta["fingerprint"])
	require.Equal(t, "uid-1", meta["uid"])
	require.Equal(t, "parent-1", meta["parent_id"])
}

// TestChunkToMetadata_OmitsEmptyOptionalFields ensures the
// conversion doesn't ship empty slices or zero timestamps as
// noisy metadata entries — the metadata map should be lean
// when the chunk is lean.
func TestChunkToMetadata_OmitsEmptyOptionalFields(t *testing.T) {
	chunk := &models.Chunk{
		ID:         "chunk-1",
		DocumentID: "doc-1",
	}

	meta := chunkToMetadata(chunk.ID, chunk)

	_, hasURLs := meta["document_urls"]
	_, hasTags := meta["document_tags"]
	_, hasCats := meta["document_categories"]
	_, hasCreated := meta["document_created_at"]
	_, hasUpdated := meta["document_updated_at"]
	_, hasFP := meta["fingerprint"]
	_, hasUID := meta["uid"]
	_, hasParent := meta["parent_id"]

	require.False(t, hasURLs, "empty URLs should not produce a metadata entry")
	require.False(t, hasTags, "empty tags should not produce a metadata entry")
	require.False(t, hasCats, "empty categories should not produce a metadata entry")
	require.False(t, hasCreated, "zero CreatedAt should not produce a metadata entry")
	require.False(t, hasUpdated, "zero UpdatedAt should not produce a metadata entry")
	require.False(t, hasFP, "empty fingerprint should not produce a metadata entry")
	require.False(t, hasUID, "empty UID should not produce a metadata entry")
	require.False(t, hasParent, "empty ParentID should not produce a metadata entry")
}

// TestMetadataToChunk_RoundTrips pins that the metadata→chunk
// conversion reverses the chunk→metadata conversion. The numeric
// fields (level/start_line/end_line) come back as ints; the
// list fields round-trip via splitNonEmptyLines.
func TestMetadataToChunk_RoundTrips(t *testing.T) {
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2024, 1, 2, 4, 5, 6, 0, time.UTC)
	original := &models.Chunk{
		ID:                 "chunk-1",
		DocumentID:         "doc-1",
		Content:            "body text",
		HeaderPath:         "Setup > Install",
		Level:              2,
		StartLine:          10,
		EndLine:            25,
		DocumentTitle:      "Doc Title",
		DocumentURLs:       []string{"https://a.example"},
		DocumentTags:       []string{"go", "rag"},
		DocumentCategories: []string{"Guides"},
		DocumentCreatedAt:  created,
		DocumentUpdatedAt:  updated,
		Fingerprint:        "fp-1",
		UID:                "uid-1",
		ParentID:           "parent-1",
	}

	meta := chunkToMetadata(original.ID, original)
	restored := metadataToChunk(meta, "body text")

	require.Equal(t, original.ID, restored.ID)
	require.Equal(t, original.DocumentID, restored.DocumentID)
	require.Equal(t, original.Content, restored.Content)
	require.Equal(t, original.HeaderPath, restored.HeaderPath)
	require.Equal(t, original.Level, restored.Level)
	require.Equal(t, original.StartLine, restored.StartLine)
	require.Equal(t, original.EndLine, restored.EndLine)
	require.Equal(t, original.DocumentTitle, restored.DocumentTitle)
	require.Equal(t, original.DocumentURLs, restored.DocumentURLs)
	require.Equal(t, original.DocumentTags, restored.DocumentTags)
	require.Equal(t, original.DocumentCategories, restored.DocumentCategories)
	require.True(t, original.DocumentCreatedAt.Equal(restored.DocumentCreatedAt))
	require.True(t, original.DocumentUpdatedAt.Equal(restored.DocumentUpdatedAt))
	require.Equal(t, original.Fingerprint, restored.Fingerprint)
	require.Equal(t, original.UID, restored.UID)
	require.Equal(t, original.ParentID, restored.ParentID)
}
