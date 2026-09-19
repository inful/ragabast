package service

import (
	"context"

	"github.com/ragabast/internal/models"
)

// ListDocuments returns all ingested documents.
func (s *Service) ListDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	docs, err := s.vectorOps.GetUniqueDocuments(ctx)
	if err != nil {
		return nil, wrapCorruptionError(err)
	}
	s.enrichDocInfos(docs)
	return docs, nil
}

// enrichDocInfos populates the DocbuilderURL field on every
// info whose UID is non-empty. Safe to call multiple times.
func (s *Service) enrichDocInfos(docs []models.DocumentInfo) {
	if s.config.Ragabast.DocbuilderBaseURL == "" {
		return
	}
	for i := range docs {
		docs[i].DocbuilderURL = s.buildDocbuilderURL(docs[i].UID)
	}
}

// GetDocument retrieves a specific document.
func (s *Service) GetDocument(ctx context.Context, documentID string) (*models.Document, error) {
	chunks, err := s.vectorOps.GetDocumentChunks(ctx, documentID)
	if err != nil {
		return nil, err
	}

	if len(chunks) == 0 {
		return nil, models.ErrNotFound
	}

	doc := &models.Document{
		ID:          documentID,
		UID:         chunks[0].UID,
		Fingerprint: chunks[0].Fingerprint,
		Title:       chunks[0].DocumentTitle,
		Chunks:      make([]models.Chunk, len(chunks)),
	}

	for i, chunk := range chunks {
		doc.Chunks[i] = *chunk
	}

	return doc, nil
}

// DeleteDocument removes a document from the system.
func (s *Service) DeleteDocument(ctx context.Context, documentID string) error {
	return s.vectorOps.DeleteDocument(ctx, documentID)
}

// GetChunk retrieves a specific chunk.
func (s *Service) GetChunk(ctx context.Context, chunkID string) (*models.Chunk, error) {
	return s.vectorOps.GetChunk(ctx, chunkID)
}

// DeleteChunk removes a specific chunk.
func (s *Service) DeleteChunk(ctx context.Context, chunkID string) error {
	return s.vectorOps.DeleteChunk(ctx, chunkID)
}
