package vector

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/philippgille/chromem-go"
	"github.com/ragabast/internal/models"
)

// VectorDB represents the vector database using chromem-go.
type VectorDB struct {
	collection         *chromem.Collection
	embeddingDimension int
	mu                 sync.RWMutex
}

// NewVectorDB creates a new vector database instance with optional persistence.
func NewVectorDB(name string, embeddingDimension int, persistenceDir string) (*VectorDB, error) {
	var db *chromem.DB

	// Create DB with persistence if directory is provided
	if persistenceDir != "" {
		// Ensure the directory exists
		if err := os.MkdirAll(persistenceDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create persistence directory: %w", err)
		}

		// Create persistent DB (compress=true for space efficiency)
		var err error
		db, err = chromem.NewPersistentDB(persistenceDir, true)
		if err != nil {
			return nil, fmt.Errorf("failed to create persistent DB: %w", err)
		}
	} else {
		// Create in-memory DB
		fmt.Println("creating in-memory DB")
		db = chromem.NewDB()
	}

	// Create a collection with a placeholder embedding function
	// The actual embeddings will be provided when adding documents
	embeddingFunc := func(ctx context.Context, text string) ([]float32, error) {
		// This is a placeholder - embeddings should be provided externally
		return make([]float32, embeddingDimension), nil
	}

	// Try to get existing collection first, create if it doesn't exist
	collection := db.GetCollection(name, embeddingFunc)
	if collection == nil {
		// Collection doesn't exist, create it
		var err error
		collection, err = db.CreateCollection(name, nil, embeddingFunc)
		if err != nil {
			return nil, fmt.Errorf("failed to create collection: %w", err)
		}
	}

	return &VectorDB{
		collection:         collection,
		embeddingDimension: embeddingDimension,
	}, nil
}

// AddChunk adds a chunk to the vector database.
func (db *VectorDB) AddChunk(ctx context.Context, chunk *models.Chunk, embedding []float32) error {
	if chunk == nil {
		return models.ErrInvalidInput
	}

	if len(embedding) == 0 {
		return models.ErrEmbeddingFailed
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	doc := chromem.Document{
		ID:        chunk.ID,
		Content:   chunk.Content,
		Embedding: embedding,
		Metadata:  chunkToMetadata(chunk.ID, chunk),
	}

	err := db.collection.AddDocument(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to add chunk to vector DB: %w", err)
	}

	return nil
}

// AddChunksBatch adds multiple chunks in a batch operation.
func (db *VectorDB) AddChunksBatch(ctx context.Context, chunks []*models.Chunk, embeddings [][]float32) error {
	if len(chunks) != len(embeddings) {
		return fmt.Errorf("%w: chunks and embeddings length mismatch", models.ErrInvalidInput)
	}

	if len(chunks) == 0 {
		return nil
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	docs := make([]chromem.Document, len(chunks))
	for i, chunk := range chunks {
		if chunk == nil {
			continue
		}

		if len(embeddings[i]) == 0 {
			return fmt.Errorf("chunk %d: %w", i, models.ErrEmbeddingFailed)
		}

		docs[i] = chromem.Document{
			ID:        chunk.ID,
			Content:   chunk.Content,
			Embedding: embeddings[i],
			Metadata:  chunkToMetadata(chunk.ID, chunk),
		}
	}

	err := db.collection.AddDocuments(ctx, docs, 1) // Use at least 1 for concurrency
	if err != nil {
		return fmt.Errorf("failed to add batch to vector DB: %w", err)
	}

	return nil
}

// Search performs a similarity search in the vector database.
func (db *VectorDB) Search(ctx context.Context, queryEmbedding []float32, limit int, filters map[string]string) ([]models.SearchResult, error) {
	if len(queryEmbedding) == 0 {
		return nil, models.ErrSearchFailed
	}

	if limit <= 0 {
		limit = 5
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	count := db.collection.Count()
	if count == 0 {
		return []models.SearchResult{}, nil
	}

	// chromem-go requires NResults <= collection.Count(), so clamp.
	maxResults := min(count, 1000)
	if limit > maxResults {
		limit = maxResults
	}
	if limit < 1 {
		limit = 1
	}

	// Build query options
	options := chromem.QueryOptions{
		QueryEmbedding: queryEmbedding,
		NResults:       limit,
	}

	// Add filters if provided
	if len(filters) > 0 {
		options.Where = filters
	}

	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}

	searchResults := make([]models.SearchResult, len(results))
	for i, result := range results {
		// Parse metadata back to proper types
		level := 0
		startLine := 0
		endLine := 0

		if val, ok := result.Metadata["level"]; ok {
			level, _ = strconv.Atoi(val)
		}
		if val, ok := result.Metadata["start_line"]; ok {
			startLine, _ = strconv.Atoi(val)
		}
		if val, ok := result.Metadata["end_line"]; ok {
			endLine, _ = strconv.Atoi(val)
		}

		searchResults[i] = models.SearchResult{
			ChunkID:            result.Metadata["chunk_id"],
			DocumentID:         result.Metadata["document_id"],
			Content:            result.Content,
			HeaderPath:         result.Metadata["header_path"],
			Level:              level,
			StartLine:          startLine,
			EndLine:            endLine,
			DocumentTitle:      result.Metadata["document_title"],
			DocumentURLs:       splitMetadataList(result.Metadata["document_urls"]),
			DocumentTags:       splitMetadataList(result.Metadata["document_tags"]),
			DocumentCategories: splitMetadataList(result.Metadata["document_categories"]),
			ParentID:           result.Metadata["parent_id"],
			Similarity:         result.Similarity,
			Fingerprint:        result.Metadata["fingerprint"],
			UID:                result.Metadata["uid"],
		}
	}

	return searchResults, nil
}

// GetChunk retrieves a specific chunk by ID.
func (db *VectorDB) GetChunk(ctx context.Context, chunkID string) (*models.Chunk, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	doc, err := db.collection.GetByID(ctx, chunkID)
	if err != nil {
		return nil, fmt.Errorf("failed to get chunk: %w", err)
	}

	return metadataToChunk(doc.Metadata, doc.Content), nil
}

// GetChunksByDocument retrieves all chunks for a specific document.
func (db *VectorDB) GetChunksByDocument(ctx context.Context, documentID string) ([]*models.Chunk, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	count := db.collection.Count()
	if count == 0 {
		return []*models.Chunk{}, nil
	}

	// Use QueryWithOptions with a filter
	// We'll use a dummy embedding and filter by document_id
	dummyEmbedding := make([]float32, db.embeddingDimension)

	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       min(count, 1000),
		Where:          map[string]string{"document_id": documentID},
	}

	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get document chunks: %w", err)
	}

	chunks := make([]*models.Chunk, len(results))
	for i, result := range results {
		chunks[i] = metadataToChunk(result.Metadata, result.Content)
	}

	return chunks, nil
}

// DeleteChunk removes a specific chunk from the database.
func (db *VectorDB) DeleteChunk(ctx context.Context, chunkID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	err := db.collection.Delete(ctx, nil, nil, chunkID)
	if err != nil {
		return fmt.Errorf("failed to delete chunk: %w", err)
	}

	return nil
}

// DeleteDocument removes all chunks for a specific document.
func (db *VectorDB) DeleteDocument(ctx context.Context, documentID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	count := db.collection.Count()
	if count == 0 {
		return nil
	}

	// Get all chunks for this document first
	dummyEmbedding := make([]float32, db.embeddingDimension)
	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       min(count, 1000),
		Where:          map[string]string{"document_id": documentID},
	}

	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return fmt.Errorf("failed to find document chunks: %w", err)
	}

	// Delete each chunk
	for _, result := range results {
		chunkID := result.Metadata["chunk_id"]
		if err := db.collection.Delete(ctx, nil, nil, chunkID); err != nil {
			return fmt.Errorf("failed to delete chunk %s: %w", chunkID, err)
		}
	}

	return nil
}

// Count returns the total number of chunks in the database.
func (db *VectorDB) Count() (int, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	return db.collection.Count(), nil
}

// splitMetadataList moved to metadata.go.

// DocumentFingerprint returns the stored fingerprint for a document if it exists.
func (db *VectorDB) DocumentFingerprint(ctx context.Context, documentID string) (string, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if strings.TrimSpace(documentID) == "" {
		return "", false, models.ErrInvalidInput
	}

	count := db.collection.Count()
	if count == 0 {
		return "", false, nil
	}

	dummyEmbedding := make([]float32, db.embeddingDimension)
	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       min(count, 1),
		Where:          map[string]string{"document_id": documentID},
	}

	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return "", false, fmt.Errorf("failed to query document fingerprint: %w", err)
	}
	if len(results) == 0 {
		return "", false, nil
	}

	fp := strings.TrimSpace(results[0].Metadata["fingerprint"])
	if fp == "" {
		return "", true, nil
	}
	return fp, true, nil
}

// DocumentNeedsUpdate returns whether an ingest should replace existing data.
//
// Rules:
// - If the document does not exist, it needs an insert (true, exists=false).
// - If it exists and fingerprint matches, no-op (false, exists=true).
// - If it exists and fingerprint differs, replace (true, exists=true).
func (db *VectorDB) DocumentNeedsUpdate(ctx context.Context, documentID string, fingerprint string) (bool, bool, error) {
	stored, exists, err := db.DocumentFingerprint(ctx, documentID)
	if err != nil {
		return false, false, err
	}
	if !exists {
		return true, false, nil
	}
	if strings.TrimSpace(stored) == strings.TrimSpace(fingerprint) {
		return false, true, nil
	}
	return true, true, nil
}

// GetUniqueDocuments returns a list of unique documents with their metadata.
func (db *VectorDB) GetUniqueDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	// Get total count first
	count := db.collection.Count()
	if count == 0 {
		return []models.DocumentInfo{}, nil
	}

	// Use a dummy embedding with the correct dimension
	dummyEmbedding := make([]float32, db.embeddingDimension)
	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       count, // Get all results
	}

	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}

	// Group by document_id to get unique documents
	docMap := make(map[string]models.DocumentInfo)
	for _, result := range results {
		docID := result.Metadata["document_id"]
		if docID == "" {
			continue
		}

		info, exists := docMap[docID]
		if !exists {
			info = models.DocumentInfo{
				ID:         docID,
				ChunkCount: 0,
			}
		}

		// Merge document-level fields from any chunk that has them.
		if info.UID == "" {
			if uid := result.Metadata["uid"]; uid != "" {
				info.UID = uid
			}
		}
		if info.Fingerprint == "" {
			if fp := result.Metadata["fingerprint"]; fp != "" {
				info.Fingerprint = fp
			}
		}
		if info.Title == "" {
			if title := result.Metadata["document_title"]; title != "" {
				info.Title = title
			}
		}
		if len(info.Tags) == 0 {
			if tags := splitNonEmptyLines(result.Metadata["document_tags"]); len(tags) > 0 {
				info.Tags = tags
			}
		}
		if len(info.Categories) == 0 {
			if cats := splitNonEmptyLines(result.Metadata["document_categories"]); len(cats) > 0 {
				info.Categories = cats
			}
		}
		if len(info.URLs) == 0 {
			if urls := splitNonEmptyLines(result.Metadata["document_urls"]); len(urls) > 0 {
				info.URLs = urls
			}
		}
		if info.CreatedAt.IsZero() {
			if created := parseRFC3339(result.Metadata["document_created_at"]); !created.IsZero() {
				info.CreatedAt = created
			}
		}
		if info.UpdatedAt.IsZero() {
			if updated := parseRFC3339(result.Metadata["document_updated_at"]); !updated.IsZero() {
				info.UpdatedAt = updated
			}
		}

		info.ChunkCount++
		docMap[docID] = info
	}

	// Convert map to slice
	docs := make([]models.DocumentInfo, 0, len(docMap))
	for _, info := range docMap {
		docs = append(docs, info)
	}

	return docs, nil
}

// splitNonEmptyLines and parseRFC3339 moved to metadata.go.

// Close cleans up resources.
func (db *VectorDB) Close() error {
	// chromem-go handles cleanup automatically
	return nil
}
