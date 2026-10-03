package service

import (
	"context"
	"fmt"
	"maps"

	"github.com/ragabast/internal/models"
)

// documentChunker is the slice of chunker.Chunker the ingest pipeline
// depends on. Keeping it private to this package lets tests supply a
// stub without dragging in the real chunker or its filesystem setup.
type documentChunker interface {
	ChunkWithHierarchy(doc *models.Document) ([]*models.Chunk, error)
}

// documentIngester is the slice of vector.VectorOperations the ingest
// pipeline depends on. Same rationale as documentChunker: tests use a
// stub; production uses *vector.VectorOperations.
type documentIngester interface {
	IngestDocument(ctx context.Context, doc *models.Document) error
}

// chunkAndIngest runs the shared "chunk this document, then hand it
// to the vector store" step that IngestFile, IngestText, and
// IngestDocument all need. Pulling it out keeps those entry points
// focused on building the *models.Document and lets each one call
// the same pipeline without copy-pasting the chunk→ingest tail.
//
// Before persistence, doc.SourceKind and doc.Metadata are
// propagated to every chunk. The chunker is source-agnostic — it
// produces chunks without SourceKind or Metadata — so this is the
// one place that copies the parent doc's source identity onto each
// child chunk. The citation dispatch and the source-kind post-
// filter rely on every chunk having a populated SourceKind; the
// vector layer's metadataToChunk also runs inferSourceKind on
// read as a backstop for legacy corpora.
//
// Errors are wrapped with the same prefixes the callers used so
// existing log lines and tests that match on the error string keep
// working.
func chunkAndIngest(ctx context.Context, ch documentChunker, ingester documentIngester, doc *models.Document) error {
	chunks, err := ch.ChunkWithHierarchy(doc)
	if err != nil {
		return fmt.Errorf("failed to chunk document: %w", err)
	}

	doc.Chunks = make([]models.Chunk, len(chunks))
	for i, chunk := range chunks {
		doc.Chunks[i] = *chunk
		doc.Chunks[i].SourceKind = doc.SourceKind
		if len(doc.Metadata) > 0 {
			if doc.Chunks[i].Metadata == nil {
				doc.Chunks[i].Metadata = make(map[string]string, len(doc.Metadata))
			}
			maps.Copy(doc.Chunks[i].Metadata, doc.Metadata)
		}
	}

	if err := ingester.IngestDocument(ctx, doc); err != nil {
		return fmt.Errorf("failed to ingest document: %w", err)
	}
	return nil
}
