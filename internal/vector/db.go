package vector

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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

	metadata := map[string]string{
		"document_id":    chunk.DocumentID,
		"chunk_id":       chunk.ID,
		"header_path":    chunk.HeaderPath,
		"level":          strconv.Itoa(chunk.Level),
		"start_line":     strconv.Itoa(chunk.StartLine),
		"end_line":       strconv.Itoa(chunk.EndLine),
		"document_title": chunk.DocumentTitle,
	}
	if len(chunk.DocumentURLs) > 0 {
		metadata["document_urls"] = strings.Join(chunk.DocumentURLs, "\n")
	}
	if len(chunk.DocumentTags) > 0 {
		metadata["document_tags"] = strings.Join(chunk.DocumentTags, "\n")
	}
	if len(chunk.DocumentCategories) > 0 {
		metadata["document_categories"] = strings.Join(chunk.DocumentCategories, "\n")
	}
	if !chunk.DocumentCreatedAt.IsZero() {
		metadata["document_created_at"] = chunk.DocumentCreatedAt.UTC().Format(time.RFC3339)
	}
	if !chunk.DocumentUpdatedAt.IsZero() {
		metadata["document_updated_at"] = chunk.DocumentUpdatedAt.UTC().Format(time.RFC3339)
	}

	// Add optional metadata if present
	if chunk.Fingerprint != "" {
		metadata["fingerprint"] = chunk.Fingerprint
	}
	if chunk.UID != "" {
		metadata["uid"] = chunk.UID
	}

	doc := chromem.Document{
		ID:        chunk.ID,
		Content:   chunk.Content,
		Embedding: embedding,
		Metadata:  metadata,
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

		metadata := map[string]string{
			"document_id":    chunk.DocumentID,
			"chunk_id":       chunk.ID,
			"header_path":    chunk.HeaderPath,
			"level":          strconv.Itoa(chunk.Level),
			"start_line":     strconv.Itoa(chunk.StartLine),
			"end_line":       strconv.Itoa(chunk.EndLine),
			"document_title": chunk.DocumentTitle,
		}
		if len(chunk.DocumentURLs) > 0 {
			metadata["document_urls"] = strings.Join(chunk.DocumentURLs, "\n")
		}
		if len(chunk.DocumentTags) > 0 {
			metadata["document_tags"] = strings.Join(chunk.DocumentTags, "\n")
		}
		if len(chunk.DocumentCategories) > 0 {
			metadata["document_categories"] = strings.Join(chunk.DocumentCategories, "\n")
		}
		if !chunk.DocumentCreatedAt.IsZero() {
			metadata["document_created_at"] = chunk.DocumentCreatedAt.UTC().Format(time.RFC3339)
		}
		if !chunk.DocumentUpdatedAt.IsZero() {
			metadata["document_updated_at"] = chunk.DocumentUpdatedAt.UTC().Format(time.RFC3339)
		}

		if chunk.Fingerprint != "" {
			metadata["fingerprint"] = chunk.Fingerprint
		}
		if chunk.UID != "" {
			metadata["uid"] = chunk.UID
		}

		docs[i] = chromem.Document{
			ID:        chunk.ID,
			Content:   chunk.Content,
			Embedding: embeddings[i],
			Metadata:  metadata,
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
			ChunkID:       result.Metadata["chunk_id"],
			DocumentID:    result.Metadata["document_id"],
			Content:       result.Content,
			HeaderPath:    result.Metadata["header_path"],
			Level:         level,
			StartLine:     startLine,
			EndLine:       endLine,
			DocumentTitle: result.Metadata["document_title"],
			DocumentURLs:  nil,
			Similarity:    result.Similarity,
			Fingerprint:   result.Metadata["fingerprint"],
			UID:           result.Metadata["uid"],
		}
		if urls, ok := result.Metadata["document_urls"]; ok && urls != "" {
			parts := strings.Split(urls, "\n")
			filtered := make([]string, 0, len(parts))
			for _, p := range parts {
				if p == "" {
					continue
				}
				filtered = append(filtered, p)
			}
			searchResults[i].DocumentURLs = filtered
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

	level := 0
	startLine := 0
	endLine := 0

	if val, ok := doc.Metadata["level"]; ok {
		level, _ = strconv.Atoi(val)
	}
	if val, ok := doc.Metadata["start_line"]; ok {
		startLine, _ = strconv.Atoi(val)
	}
	if val, ok := doc.Metadata["end_line"]; ok {
		endLine, _ = strconv.Atoi(val)
	}

	return &models.Chunk{
		ID:                 doc.Metadata["chunk_id"],
		DocumentID:         doc.Metadata["document_id"],
		Content:            doc.Content,
		HeaderPath:         doc.Metadata["header_path"],
		Level:              level,
		StartLine:          startLine,
		EndLine:            endLine,
		DocumentTitle:      doc.Metadata["document_title"],
		Fingerprint:        doc.Metadata["fingerprint"],
		UID:                doc.Metadata["uid"],
		DocumentURLs:       splitNonEmptyLines(doc.Metadata["document_urls"]),
		DocumentTags:       splitNonEmptyLines(doc.Metadata["document_tags"]),
		DocumentCategories: splitNonEmptyLines(doc.Metadata["document_categories"]),
		DocumentCreatedAt:  parseRFC3339(doc.Metadata["document_created_at"]),
		DocumentUpdatedAt:  parseRFC3339(doc.Metadata["document_updated_at"]),
	}, nil
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

		chunks[i] = &models.Chunk{
			ID:                 result.Metadata["chunk_id"],
			DocumentID:         result.Metadata["document_id"],
			Content:            result.Content,
			HeaderPath:         result.Metadata["header_path"],
			Level:              level,
			StartLine:          startLine,
			EndLine:            endLine,
			DocumentTitle:      result.Metadata["document_title"],
			Fingerprint:        result.Metadata["fingerprint"],
			UID:                result.Metadata["uid"],
			DocumentURLs:       splitNonEmptyLines(result.Metadata["document_urls"]),
			DocumentTags:       splitNonEmptyLines(result.Metadata["document_tags"]),
			DocumentCategories: splitNonEmptyLines(result.Metadata["document_categories"]),
			DocumentCreatedAt:  parseRFC3339(result.Metadata["document_created_at"]),
			DocumentUpdatedAt:  parseRFC3339(result.Metadata["document_updated_at"]),
		}
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
				ID:          docID,
				UID:         result.Metadata["uid"],
				Fingerprint: result.Metadata["fingerprint"],
				Title:       result.Metadata["document_title"],
				Tags:        splitNonEmptyLines(result.Metadata["document_tags"]),
				Categories:  splitNonEmptyLines(result.Metadata["document_categories"]),
				URLs:        splitNonEmptyLines(result.Metadata["document_urls"]),
				CreatedAt:   parseRFC3339(result.Metadata["document_created_at"]),
				UpdatedAt:   parseRFC3339(result.Metadata["document_updated_at"]),
				ChunkCount:  0,
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

func splitNonEmptyLines(s string) []string {
	if s == "" {
		return []string{}
	}

	parts := strings.Split(s, "\n")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered
}

func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// Close cleans up resources.
func (db *VectorDB) Close() error {
	// chromem-go handles cleanup automatically
	return nil
}
