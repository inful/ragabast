package vector

import (
	"context"
	"errors"
	"fmt"

	"github.com/ragabast/internal/models"
)

// VectorOperations handles high-level vector database operations.
type VectorOperations struct {
	db         *VectorDB
	embeddings *OllamaEmbeddingClient
}

// NewVectorOperations creates a new vector operations handler.
func NewVectorOperations(db *VectorDB, embeddings *OllamaEmbeddingClient) *VectorOperations {
	return &VectorOperations{
		db:         db,
		embeddings: embeddings,
	}
}

// IngestChunk processes a single chunk: generates embedding and stores it.
func (vo *VectorOperations) IngestChunk(ctx context.Context, chunk *models.Chunk) error {
	if chunk == nil {
		return models.ErrInvalidInput
	}

	// Generate embedding
	embedding, err := vo.embeddings.GenerateChunkEmbedding(ctx, chunk)
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Store in vector DB
	err = vo.db.AddChunk(ctx, chunk, embedding)
	if err != nil {
		return fmt.Errorf("failed to store chunk: %w", err)
	}

	return nil
}

// IngestDocument processes all chunks of a document.
func (vo *VectorOperations) IngestDocument(ctx context.Context, doc *models.Document) error {
	if doc == nil {
		return models.ErrInvalidInput
	}

	// Validate document
	if err := doc.Validate(); err != nil {
		return fmt.Errorf("document validation failed: %w", err)
	}

	// If this document was ingested before, remove old chunks first.
	// With deterministic IDs, this prevents duplicate-ID insert failures and makes re-ingest idempotent.
	if err := vo.db.DeleteDocument(ctx, doc.ID); err != nil {
		return fmt.Errorf("failed to clear existing document %s: %w", doc.ID, err)
	}

	// Generate embeddings for all chunks
	chunks := make([]*models.Chunk, len(doc.Chunks))
	for i := range doc.Chunks {
		chunks[i] = &doc.Chunks[i]
		// Add document-level metadata to chunks if not already set
		if chunks[i].DocumentTitle == "" {
			chunks[i].DocumentTitle = doc.Title
		}
		if chunks[i].Fingerprint == "" {
			chunks[i].Fingerprint = doc.Fingerprint
		}
		if chunks[i].UID == "" {
			chunks[i].UID = doc.UID
		}
		if len(chunks[i].DocumentURLs) == 0 {
			chunks[i].DocumentURLs = doc.URLs
		}
		if len(chunks[i].DocumentTags) == 0 {
			chunks[i].DocumentTags = doc.Tags
		}
		if len(chunks[i].DocumentCategories) == 0 {
			chunks[i].DocumentCategories = doc.Categories
		}
		if chunks[i].DocumentCreatedAt.IsZero() {
			chunks[i].DocumentCreatedAt = doc.CreatedAt
		}
		if chunks[i].DocumentUpdatedAt.IsZero() {
			chunks[i].DocumentUpdatedAt = doc.UpdatedAt
		}
	}

	embeddings, err := vo.embeddings.GenerateChunkEmbeddings(ctx, chunks)
	if err != nil {
		return fmt.Errorf("failed to generate embeddings: %w", err)
	}

	// Store all chunks
	err = vo.db.AddChunksBatch(ctx, chunks, embeddings)
	if err != nil {
		return fmt.Errorf("failed to store document chunks: %w", err)
	}

	return nil
}

// Search performs a semantic search across all documents.
func (vo *VectorOperations) Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error) {
	if query == "" {
		return nil, models.ErrSearchFailed
	}

	// Generate query embedding
	embedding, err := vo.embeddings.GenerateEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search vector DB
	results, err := vo.db.Search(ctx, embedding, limit, filters)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	return results, nil
}

// SearchByDocument searches within a specific document.
func (vo *VectorOperations) SearchByDocument(ctx context.Context, query string, documentID string, limit int) ([]models.SearchResult, error) {
	filters := map[string]string{
		"document_id": documentID,
	}
	return vo.Search(ctx, query, limit, filters)
}

// GetDocumentChunks retrieves all chunks for a document.
func (vo *VectorOperations) GetDocumentChunks(ctx context.Context, documentID string) ([]*models.Chunk, error) {
	return vo.db.GetChunksByDocument(ctx, documentID)
}

// GetChunk retrieves a specific chunk by ID.
func (vo *VectorOperations) GetChunk(ctx context.Context, chunkID string) (*models.Chunk, error) {
	return vo.db.GetChunk(ctx, chunkID)
}

// DeleteDocument removes all data for a document.
func (vo *VectorOperations) DeleteDocument(ctx context.Context, documentID string) error {
	return vo.db.DeleteDocument(ctx, documentID)
}

// DeleteChunk removes a specific chunk.
func (vo *VectorOperations) DeleteChunk(ctx context.Context, chunkID string) error {
	return vo.db.DeleteChunk(ctx, chunkID)
}

// GetStats returns statistics about the vector database.
func (vo *VectorOperations) GetStats() (int, error) {
	return vo.db.Count()
}

// ValidateConnection checks if all services are accessible.
func (vo *VectorOperations) ValidateConnection(ctx context.Context) error {
	// Check Ollama
	if err := vo.embeddings.ValidateConnection(ctx); err != nil {
		return fmt.Errorf("Ollama connection failed: %w", err)
	}

	// Check vector DB (basic operation)
	_, err := vo.db.Count()
	if err != nil {
		return fmt.Errorf("vector DB access failed: %w", err)
	}

	return nil
}

// GetUniqueDocuments returns a list of unique documents.
func (vo *VectorOperations) GetUniqueDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	return vo.db.GetUniqueDocuments(ctx)
}

// RebuildEmbeddings regenerates embeddings for all chunks (useful for model changes).
func (vo *VectorOperations) RebuildEmbeddings(ctx context.Context) error {
	// Get all document IDs first (this would require additional methods)
	// For now, this is a placeholder for future implementation
	return errors.New("rebuild embeddings not yet implemented")
}
