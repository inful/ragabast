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
