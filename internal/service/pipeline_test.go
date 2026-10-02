package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// fakeChunker returns a fixed chunk list and (optionally) an error,
// without depending on the real chunker package.
type fakeChunker struct {
	chunks []*models.Chunk
	err    error
}

func (f *fakeChunker) ChunkWithHierarchy(_ *models.Document) ([]*models.Chunk, error) {
	return f.chunks, f.err
}

// fakeIngester captures the document it was asked to ingest and
// (optionally) returns an error.
type fakeIngester struct {
	received *models.Document
	calls    int
	err      error
}

func (f *fakeIngester) IngestDocument(_ context.Context, doc *models.Document) error {
	f.received = doc
	f.calls++
	return f.err
}

func TestChunkAndIngest_CopiesChunksOntoDocument(t *testing.T) {
	doc := &models.Document{ID: "d1"}
	ch := &fakeChunker{chunks: []*models.Chunk{{ID: "c1", Content: "one"}, {ID: "c2", Content: "two"}}}
	ing := &fakeIngester{}

	err := chunkAndIngest(t.Context(), ch, ing, doc)

	require.NoError(t, err)
	require.Len(t, doc.Chunks, 2, "chunks should be copied onto the document by value")
	require.Equal(t, "c1", doc.Chunks[0].ID)
	require.Equal(t, "c2", doc.Chunks[1].ID)
}

func TestChunkAndIngest_PassesDocumentToIngester(t *testing.T) {
	doc := &models.Document{ID: "d42"}
	ch := &fakeChunker{chunks: []*models.Chunk{{ID: "c1"}}}
	ing := &fakeIngester{}

	err := chunkAndIngest(t.Context(), ch, ing, doc)

	require.NoError(t, err)
	require.Same(t, doc, ing.received, "ingester should receive the same document instance")
	require.Equal(t, 1, ing.calls)
}

func TestChunkAndIngest_ChunkerErrorReturned(t *testing.T) {
	doc := &models.Document{ID: "d1"}
	wantErr := errors.New("boom")
	ch := &fakeChunker{err: wantErr}
	ing := &fakeIngester{}

	err := chunkAndIngest(t.Context(), ch, ing, doc)

	require.ErrorIs(t, err, wantErr)
	require.Empty(t, doc.Chunks, "no chunks should be assigned when chunking fails")
	require.Equal(t, 0, ing.calls, "ingester must not be called when chunking fails")
}

func TestChunkAndIngest_IngesterErrorReturned(t *testing.T) {
	doc := &models.Document{ID: "d1"}
	ch := &fakeChunker{chunks: []*models.Chunk{{ID: "c1"}}}
	wantErr := errors.New("vector db offline")
	ing := &fakeIngester{err: wantErr}

	err := chunkAndIngest(t.Context(), ch, ing, doc)

	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 1, ing.calls)
}

// TestChunkAndIngest_PropagatesSourceKindToChunks pins the
// contract that doc.SourceKind is copied onto every chunk
// produced by chunkAndIngest. The chunker itself is
// source-agnostic; this is the one place that wires the parent
// document's source identity onto each child chunk. The citation
// dispatch and the source-kind post-filter both rely on every
// chunk carrying a populated SourceKind.
func TestChunkAndIngest_PropagatesSourceKindToChunks(t *testing.T) {
	doc := &models.Document{
		ID:         "d1",
		SourceKind: models.SourceGitLab,
	}
	ch := &fakeChunker{chunks: []*models.Chunk{
		{ID: "c1"}, {ID: "c2"}, {ID: "c3"},
	}}
	ing := &fakeIngester{}

	err := chunkAndIngest(t.Context(), ch, ing, doc)
	require.NoError(t, err)
	for i, c := range ing.received.Chunks {
		require.Equal(t, models.SourceGitLab, c.SourceKind,
			"chunk[%d] (%s) must inherit doc.SourceKind", i, c.ID)
	}
}

// TestChunkAndIngest_PropagatesMetadataToChunks pins that
// doc.Metadata is copied onto every chunk's Metadata. The
// gitlab writer populates doc.Metadata via the parser's generic
// "extra fields" pass (state, author_username); the chunk needs
// the same keys so the source-specific post-filter can scope a
// retrieval (e.g. only open issues).
func TestChunkAndIngest_PropagatesMetadataToChunks(t *testing.T) {
	doc := &models.Document{
		ID: "d1",
		Metadata: map[string]string{
			"state":           "opened",
			"author_username": "alice",
		},
	}
	ch := &fakeChunker{chunks: []*models.Chunk{{ID: "c1"}}}
	ing := &fakeIngester{}

	err := chunkAndIngest(t.Context(), ch, ing, doc)
	require.NoError(t, err)
	require.Equal(t, "opened", ing.received.Chunks[0].Metadata["state"],
		"chunk.Metadata must carry doc.Metadata['state']")
	require.Equal(t, "alice", ing.received.Chunks[0].Metadata["author_username"],
		"chunk.Metadata must carry doc.Metadata['author_username']")
}

// TestChunkAndIngest_NoPropagationWhenDocIsBare pins the no-op
// case: a doc with empty SourceKind and nil Metadata produces
// chunks with empty SourceKind and nil Metadata. Existing
// pre-SourceKind corpora flow through this path; the post-filter
// sees SourceUnknown and treats it as SourceDocbuilder per the
// spec's backwards-compat rule.
func TestChunkAndIngest_NoPropagationWhenDocIsBare(t *testing.T) {
	doc := &models.Document{ID: "d1"}
	ch := &fakeChunker{chunks: []*models.Chunk{{ID: "c1"}}}
	ing := &fakeIngester{}

	err := chunkAndIngest(t.Context(), ch, ing, doc)
	require.NoError(t, err)
	require.Equal(t, models.SourceUnknown, ing.received.Chunks[0].SourceKind,
		"chunk.SourceKind must default to SourceUnknown when doc.SourceKind is empty")
	require.Nil(t, ing.received.Chunks[0].Metadata,
		"chunk.Metadata must be nil when doc.Metadata is nil (no allocation, omitempty drops the field)")
}
