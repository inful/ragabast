package vector

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	embeddingModel     string
	persistenceDir     string
	mu                 sync.RWMutex
}

// Options is the constructor input for NewVectorDBWithOptions.
// The simpler NewVectorDB(name, dim, dir, model) helper builds
// an Options under the hood for the common case; Options
// exists so callers can flip FailOnModelMismatch without
// growing the positional-argument list further.
type Options struct {
	Name               string
	EmbeddingDimension int
	PersistenceDir     string
	EmbeddingModel     string

	// FailOnModelMismatch controls what happens when the
	// persisted model's marker file carries a different
	// model name than EmbeddingModel. True (the default)
	// returns an error from the constructor; false logs a
	// warning and proceeds. The override is intended for
	// operators who have manually migrated the data (or who
	// accept the risk during a phased rollout).
	FailOnModelMismatch bool
}

// modelMarkerFile is the on-disk marker that records which
// embedding model produced the vectors in this persistence
// directory. Read on every constructor call; written on the
// first successful AddChunk / AddChunksBatch.
//
// Lives at the persistence root (next to chromem-go's own
// files) so a single ls of the directory answers the
// "what model is this?" question.
const modelMarkerFile = ".embedding_model"

// ErrModelMismatch is returned (wrapped) by NewVectorDB when
// the persisted marker carries a different model name than the
// configured one and FailOnModelMismatch is true. Callers
// can errors.Is(err, ErrModelMismatch) to detect.
var ErrModelMismatch = errors.New("vector DB embedding model mismatch")

// NewVectorDB creates a new vector database instance. It is
// shorthand for NewVectorDBWithOptions with FailOnModelMismatch
// set to true (the safe default).
//
// The persistence directory, when non-empty, must contain
// vectors from EmbeddingModel. A model swap requires wiping
// the directory (`ragabast vector reset --force`) or setting
// FailOnModelMismatch=false in the Options form.
func NewVectorDB(name string, embeddingDimension int, persistenceDir, embeddingModel string) (*VectorDB, error) {
	return NewVectorDBWithOptions(Options{
		Name:                name,
		EmbeddingDimension:  embeddingDimension,
		PersistenceDir:      persistenceDir,
		EmbeddingModel:      embeddingModel,
		FailOnModelMismatch: true,
	})
}

// NewVectorDBWithOptions constructs the DB with full control
// over the failure mode. See Options for the field meanings.
func NewVectorDBWithOptions(opts Options) (*VectorDB, error) {
	var db *chromem.DB

	// Create DB with persistence if directory is provided
	if opts.PersistenceDir != "" {
		// Ensure the directory exists
		if err := os.MkdirAll(opts.PersistenceDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create persistence directory: %w", err)
		}

		// Check the model marker before opening chromem-go.
		// We do this first so a mismatch returns the cleanest
		// error path (no chromem-go state to clean up).
		if err := checkModelMarker(opts.PersistenceDir, opts.EmbeddingModel, opts.FailOnModelMismatch); err != nil {
			return nil, err
		}

		// Create persistent DB (compress=true for space efficiency)
		var err error
		db, err = chromem.NewPersistentDB(opts.PersistenceDir, true)
		if err != nil {
			return nil, fmt.Errorf("failed to create persistent DB: %w", err)
		}
	} else {
		// Create in-memory DB
		log.Printf("vector: in-memory database (no persistence_dir configured)")
		db = chromem.NewDB()
	}

	// Create a collection with a placeholder embedding function
	// The actual embeddings will be provided when adding documents
	embeddingFunc := func(ctx context.Context, text string) ([]float32, error) {
		// This is a placeholder - embeddings should be provided externally
		return make([]float32, opts.EmbeddingDimension), nil
	}

	// Try to get existing collection first, create if it doesn't exist
	collection := db.GetCollection(opts.Name, embeddingFunc)
	if collection == nil {
		// Collection doesn't exist, create it
		var err error
		collection, err = db.CreateCollection(opts.Name, nil, embeddingFunc)
		if err != nil {
			return nil, fmt.Errorf("failed to create collection: %w", err)
		}
	}

	return &VectorDB{
		collection:         collection,
		embeddingDimension: opts.EmbeddingDimension,
		embeddingModel:     opts.EmbeddingModel,
		persistenceDir:     opts.PersistenceDir,
	}, nil
}

// checkModelMarker reads the on-disk marker file at persistDir
// and compares its contents to configuredModel. Behavior:
//
//   - Marker missing: nothing to check. Return nil. The first
//     AddChunk call will write the marker.
//   - Marker present, matches configured: return nil.
//   - Marker present, differs: if failOnMismatch is true,
//     return an error wrapping ErrModelMismatch with both
//     names. Otherwise log a warning and return nil so the
//     constructor succeeds.
func checkModelMarker(persistDir, configuredModel string, failOnMismatch bool) error {
	path := filepath.Join(persistDir, modelMarkerFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read model marker at %s: %w", path, err)
	}
	persisted := strings.TrimSpace(string(data))
	if persisted == configuredModel {
		return nil
	}
	msg := fmt.Sprintf(
		"persisted embedding model %q differs from configured %q; "+
			"vectors from %q cannot be searched safely under %q",
		persisted, configuredModel, persisted, configuredModel)
	if failOnMismatch {
		return fmt.Errorf("%w: %s; recover with `ragabast vector reset --force` "+
			"then `ragabast ingest` to rebuild from source documents", ErrModelMismatch, msg)
	}
	log.Printf("⚠ %s; proceeding because FailOnModelMismatch=false", msg)
	return nil
}

// writeModelMarkerIfMissing stamps the embedding model on the
// persistence directory the first time data is written.
// Idempotent: subsequent calls observe the existing file and
// do not re-write. The marker is only written when a chunk
// successfully lands in the collection — a failed write
// must not commit a model claim on data that isn't there.
//
// Does NOT take db.mu — the caller (AddChunk / AddChunksBatch)
// already holds it, and the os.Stat + os.WriteFile sequence
// is atomic enough on every supported filesystem for the
// first-writer-wins property we need here.
func (db *VectorDB) writeModelMarkerIfMissing() {
	if db == nil || db.embeddingModel == "" {
		return
	}
	if db.persistenceDir == "" {
		return
	}
	path := filepath.Join(db.persistenceDir, modelMarkerFile)
	if _, err := os.Stat(path); err == nil {
		// Marker already exists — model claim already
		// recorded. Don't re-write; the caller's intent is
		// only "make sure there's a marker".
		return
	}
	data := []byte(db.embeddingModel)
	// 0644: readable by the operator running `ls`; not
	// world-writable because the marker is integrity
	// information, not a shared resource.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("vector: failed to write model marker at %s: %v", path, err)
	}
}

// AddChunk adds a chunk to the vector database.
func (db *VectorDB) AddChunk(ctx context.Context, chunk *models.Chunk, embedding []float32) error {
	if chunk == nil {
		return models.ErrInvalidInput
	}

	if len(embedding) == 0 {
		return models.ErrEmbeddingFailed
	}

	if err := db.checkEmbeddingDimension(len(embedding)); err != nil {
		return err
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

	// Stamp the embedding model on the persistence dir the
	// first time data lands. The marker survives across
	// restarts so subsequent opens can verify the model.
	db.writeModelMarkerIfMissing()
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

	for i, embedding := range embeddings {
		if err := db.checkEmbeddingDimension(len(embedding)); err != nil {
			return fmt.Errorf("chunk %d: %w", i, err)
		}
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

// checkEmbeddingDimension rejects an embedding whose length does
// not match the collection's configured dimension. Without this
// check, a chromem-go similarity calculation later panics with
// "vectors must have the same length" when a query hits a chunk
// whose shape differs from the rest of the collection.
//
// The realistic failure mode this prevents:
//  1. User ingests with embedding_dimensions: 0 (full size, e.g.
//     768 for nomic-embed-text-v1.5).
//  2. User flips embedding_dimensions: 256 (Matryoshka) and
//     re-ingests without wiping data/vectors/.
//  3. New chunks land at 256 dims; old chunks stay at 768.
//  4. Search compares a 768-dim vector against a 256-dim vector.
//     chromem-go returns "vectors must have the same length"
//     and the search request 500s.
//
// The error message has to diagnose two distinct causes with the
// same symptom:
//
//	A. Config is wrong (vectordb.embedding_dimension does not
//	   match what the current embedding model returns). Wiping
//	   data/vectors/ does NOT help; only the config does.
//	B. Stored data is stale (config unchanged but a previous
//	   ingest wrote vectors of a different length). Wiping
//	   data/vectors/ + re-ingest does help.
//
// We can't tell A from B at the check site (the on-disk store
// may be empty after a wipe), so the message names both paths
// and points at `ragabast doctor` for a quick diagnosis.
func (db *VectorDB) checkEmbeddingDimension(actual int) error {
	if actual == db.embeddingDimension {
		return nil
	}
	return fmt.Errorf(
		"%w: chunk embedding has length %d but the collection is configured for %d. "+
			"Two possible causes: (A) your config is wrong — set vectordb.embedding_dimension "+
			"to %d, or change ollama.embedding_model to one that returns %d-dim vectors; "+
			"(B) your stored data is stale from a previous model — run `ragabast vector reset --force` "+
			"and re-ingest. Run `ragabast doctor` for a diagnosis.",
		models.ErrEmbeddingDimensionMismatch, actual, db.embeddingDimension, actual, db.embeddingDimension,
	)
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
		// Document-level timestamps for date-range filtering
		// (#38). parseRFC3339 returns the zero time on a
		// missing or malformed field; only assign to the
		// result when the timestamp is actually parseable, so
		// a result with a missing field has nil dates
		// (post-filter treats nil as "no constraint").
		if created := parseRFC3339(result.Metadata["document_created_at"]); !created.IsZero() {
			searchResults[i].DocumentCreatedAt = &created
		}
		if updated := parseRFC3339(result.Metadata["document_updated_at"]); !updated.IsZero() {
			searchResults[i].DocumentUpdatedAt = &updated
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
//
// Returns vector.ErrNotFound when no chunks match the
// document_id. The HTTP layer relies on this to map the
// delete-on-missing-ID case to 404 without an extra
// existence-check round trip — see issue #9 (the N+1 fix).
// The sentinel is matched with errors.Is so wrapped errors
// propagate correctly.
func (db *VectorDB) DeleteDocument(ctx context.Context, documentID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	count := db.collection.Count()
	if count == 0 {
		// Whole collection is empty — nothing matches.
		return ErrNotFound
	}

	// Get all chunks for this document first.
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

	// No rows matched the document_id filter — the document
	// doesn't exist in this collection.
	if len(results) == 0 {
		return ErrNotFound
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

// SampleEmbeddingLength returns the length of the first stored
// embedding in the collection, plus a "found" flag that is true
// only when the collection has at least one chunk. Empty stores
// return (0, false, nil); callers can distinguish "no data yet"
// from "data exists, length N".
//
// Used by `ragabast doctor` to detect a stale-data mismatch:
// the operator's config says the collection is configured for
// vectordb.embedding_dimension N, but the on-disk store still
// holds vectors of length M from a previous model. Comparing
// the two is the only way to catch this — the model/dim table
// check (cmd/doctor_models.go) only catches config-level
// mismatches.
func (db *VectorDB) SampleEmbeddingLength(ctx context.Context) (int, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	count := db.collection.Count()
	if count == 0 {
		return 0, false, nil
	}

	// Use a dummy embedding of the configured dim to pull back
	// one document. This will FAIL (chromem-go returns
	// "vectors must have the same length") if the collection has
	// mixed dims internally; that's actually the case we want to
	// surface to the user, not paper over. The error message at
	// the higher level points at vector reset --force.
	dummyEmbedding := make([]float32, db.embeddingDimension)
	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       1,
	}

	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return 0, false, fmt.Errorf("sample query failed: %w", err)
	}
	if len(results) == 0 {
		return 0, false, nil
	}
	return len(results[0].Embedding), true, nil
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
// GetUniqueDocuments returns every unique document. It is
// kept as a backward-compatible wrapper for callers that
// genuinely need the full list (the prune endpoint, which
// has to walk every doc to build its keep/delete plan).
// New callers that only want a page should use
// GetUniqueDocumentsPaged.
func (db *VectorDB) GetUniqueDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	docs, _, err := db.GetUniqueDocumentsPaged(ctx, 0, 0)
	return docs, err
}

// GetUniqueDocumentsPaged returns a page of unique
// documents plus the total corpus size. limit=0 means
// "no limit" (return everything). offset is clamped to
// the [0, total] range. Results are sorted by document
// ID so the same offset returns the same first row on
// every request — without sorting, pagination would
// shift every time the map iteration order changed.
//
// The full collection walk is unavoidable: chromem-go
// does not store per-document metadata, so the only way
// to enumerate unique documents is to scan chunks and
// dedupe by document_id. The win from pagination is
// bounded response size (the page slice) and bounded
// downstream work (enrichment, JSON serialization), not
// bounded vector-DB work.
func (db *VectorDB) GetUniqueDocumentsPaged(ctx context.Context, limit, offset int) ([]models.DocumentInfo, int, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	// Get total count first
	count := db.collection.Count()
	if count == 0 {
		return []models.DocumentInfo{}, 0, nil
	}

	// Use a dummy embedding with the correct dimension
	dummyEmbedding := make([]float32, db.embeddingDimension)
	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       count, // Get all results
	}

	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query documents: %w", err)
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

	// Convert map to slice and sort by ID. Sorting is the
	// cheap part (O(D log D) on unique docs, where D ≪ N_chunks);
	// it's what makes pagination stable.
	all := make([]models.DocumentInfo, 0, len(docMap))
	for _, info := range docMap {
		all = append(all, info)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	total := len(all)

	// Clamp offset to [0, total]. Negative offsets are
	// treated as 0 — same input-validation policy as the
	// HTTP layer will apply upstream.
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}

	// Compute the page window. limit <= 0 means "no limit".
	end := total
	if limit > 0 {
		end = min(offset+limit, total)
	}

	page := all[offset:end]
	return page, total, nil
}

// splitNonEmptyLines and parseRFC3339 moved to metadata.go.

// Close cleans up resources.
func (db *VectorDB) Close() error {
	// chromem-go handles cleanup automatically
	return nil
}
