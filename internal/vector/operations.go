package vector

import (
	"context"
	"fmt"

	"github.com/ragabast/internal/models"
)

// VectorOperations handles high-level vector database operations.
// It owns two parallel stores: the embedding vector DB and the
// bleve-backed text index. Both stores are kept in sync on every
// ingest, delete, and clear operation. When searchIndex is nil
// (the historical default), the keyword side degrades gracefully
// — the vector side keeps working unchanged.
type VectorOperations struct {
	db          *VectorDB
	embeddings  *OpenAIEmbeddingClient
	searchIndex *SearchIndex
}

// NewVectorOperations creates a new vector operations handler.
// Call SetSearchIndex to enable the keyword index for hybrid
// search; otherwise only semantic search is available.
func NewVectorOperations(db *VectorDB, embeddings *OpenAIEmbeddingClient) *VectorOperations {
	return &VectorOperations{
		db:         db,
		embeddings: embeddings,
	}
}

// SetSearchIndex wires the bleve-backed keyword index into the
// lifecycle. Once set, every IngestDocument / DeleteDocument /
// DeleteChunk keeps both stores in sync. Calling with nil
// disables the keyword side; existing writes still complete on
// the vector side.
func (vo *VectorOperations) SetSearchIndex(idx *SearchIndex) {
	vo.searchIndex = idx
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

	needsUpdate, exists, err := vo.db.DocumentNeedsUpdate(ctx, doc.ID, doc.Fingerprint)
	if err != nil {
		return fmt.Errorf("failed to check existing document %s: %w", doc.ID, err)
	}
	if exists && !needsUpdate {
		return nil
	}
	if exists && needsUpdate {
		// Clear old chunks before re-ingest.
		// With deterministic IDs, this prevents duplicate-ID insert failures and makes re-ingest idempotent.
		// Clear both stores — the keyword index has its own
		// chunks and would otherwise leave stale entries that
		// the vector DB no longer reports.
		if deleteErr := vo.db.DeleteDocument(ctx, doc.ID); deleteErr != nil {
			return fmt.Errorf("failed to clear existing document %s: %w", doc.ID, deleteErr)
		}
		if vo.searchIndex != nil {
			if deleteErr := vo.searchIndex.DeleteDocument(doc.ID); deleteErr != nil {
				return fmt.Errorf("failed to clear existing document from search index %s: %w", doc.ID, deleteErr)
			}
		}
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

	// Mirror every chunk into the keyword index when one
	// is wired. Failure here surfaces to the caller; the
	// vector DB write already committed, but the next
	// re-ingest will hit the needsUpdate path and clean
	// up both stores (so partial state self-heals).
	if vo.searchIndex != nil {
		summaries := make([]chunkSummary, 0, len(chunks))
		for _, c := range chunks {
			summaries = append(summaries, chunkSummary{
				ID:         c.ID,
				Content:    c.Content,
				Title:      c.DocumentTitle,
				DocumentID: c.DocumentID,
				Tags:       c.DocumentTags,
				Categories: c.DocumentCategories,
			})
		}
		if addErr := vo.searchIndex.AddBatch(summaries); addErr != nil {
			return fmt.Errorf("failed to index chunks for keyword search: %w", addErr)
		}
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

// GetDocumentChunks retrieves all chunks for a document.
func (vo *VectorOperations) GetDocumentChunks(ctx context.Context, documentID string) ([]*models.Chunk, error) {
	return vo.db.GetChunksByDocument(ctx, documentID)
}

// GetChunk retrieves a specific chunk by ID.
func (vo *VectorOperations) GetChunk(ctx context.Context, chunkID string) (*models.Chunk, error) {
	return vo.db.GetChunk(ctx, chunkID)
}

// DeleteDocument removes all data for a document from both
// the vector DB and the keyword index.
func (vo *VectorOperations) DeleteDocument(ctx context.Context, documentID string) error {
	if err := vo.db.DeleteDocument(ctx, documentID); err != nil {
		return err
	}
	if vo.searchIndex != nil {
		if err := vo.searchIndex.DeleteDocument(documentID); err != nil {
			return fmt.Errorf("delete from search index: %w", err)
		}
	}
	return nil
}

// DeleteChunk removes a single chunk from both stores.
func (vo *VectorOperations) DeleteChunk(ctx context.Context, chunkID string) error {
	if err := vo.db.DeleteChunk(ctx, chunkID); err != nil {
		return err
	}
	if vo.searchIndex != nil {
		if err := vo.searchIndex.DeleteChunk(chunkID); err != nil {
			return fmt.Errorf("delete from search index: %w", err)
		}
	}
	return nil
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

// GetUniqueDocuments returns every unique document. Kept
// for backward compatibility; new callers should use
// GetUniqueDocumentsPaged.
func (vo *VectorOperations) GetUniqueDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	return vo.db.GetUniqueDocuments(ctx)
}

// GetUniqueDocumentsPaged returns a page of unique
// documents plus the total corpus size. limit=0 means
// "no limit" (return everything). See VectorDB for the
// full rationale on why the chunk walk is unavoidable.
func (vo *VectorOperations) GetUniqueDocumentsPaged(ctx context.Context, limit, offset int) ([]models.DocumentInfo, int, error) {
	return vo.db.GetUniqueDocumentsPaged(ctx, limit, offset)
}
