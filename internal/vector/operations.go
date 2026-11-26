package vector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/ragabast/internal/models"
)

// VectorOperations handles high-level vector database operations.
// It owns two parallel stores: the embedding vector DB and the
// bleve-backed text index. Both stores are kept in sync on every
// ingest, delete, and clear operation. When searchIndex is nil
// (the historical default), the keyword side degrades gracefully
// — the vector side keeps working unchanged.
//
// embeddings is an EmbeddingClient (interface) so the same
// VectorOperations can serve either the OpenAI-compat path
// (Ollama, vLLM, LM Studio, llama.cpp, OpenAI) or the
// EmbeddingGemma /v2/embed path, depending on which
// concrete client service.NewService wires in.
type VectorOperations struct {
	db          *VectorDB
	embeddings  EmbeddingClient
	searchIndex *SearchIndex

	// ingestMu serializes concurrent ingest on the same
	// doc.ID. The check-then-write race (audit finding 4)
	// was: T1 and T2 both see exists+needsUpdate, T1 deletes
	// + adds, T2 deletes (clearing T1's add) + adds. The
	// deterministic chunk IDs save the final state from
	// being double-written, but interleaving can still
	// obscure the self-heal claim and complicate debugging.
	//
	// Map entries are kept for the life of the process. The
	// bound is the number of DISTINCT doc.IDs ingested, not the
	// number of ingest calls: re-ingesting a document reuses its
	// entry, so a corpus of N documents costs N entries once.
	//
	// Entries are not released when a document is deleted.
	// Removing a mutex from the map while another goroutine may
	// be blocked on it is the classic "delete a lock someone is
	// waiting on" bug: the waiter holds a pointer to a mutex
	// that is no longer the one the map hands out, and the next
	// caller gets a fresh mutex — the two then run concurrently
	// and the serialization this lock exists to provide is
	// silently lost. The map is rebuilt from scratch on restart,
	// so the growth is bounded by one process's ingest history.
	//
	// The per-entry cost is the ID string plus a mutex, roughly
	// 60 bytes for a UUID — negligible next to the document
	// and its chunks.
	ingestMu     sync.Mutex // guards ingestMuByID
	ingestMuByID map[string]*sync.Mutex
}

// lockForDoc returns (and lazily creates) the per-doc
// mutex for id. The lookup is quick and never errors; the
// returned mutex is always non-nil.
func (vo *VectorOperations) lockForDoc(id string) *sync.Mutex {
	vo.ingestMu.Lock()
	defer vo.ingestMu.Unlock()
	m, ok := vo.ingestMuByID[id]
	if !ok {
		m = &sync.Mutex{}
		if vo.ingestMuByID == nil {
			vo.ingestMuByID = make(map[string]*sync.Mutex)
		}
		vo.ingestMuByID[id] = m
	}
	return m
}

// NewVectorOperations creates a new vector operations handler.
// Call SetSearchIndex to enable the keyword index for hybrid
// search; otherwise only semantic search is available.
func NewVectorOperations(db *VectorDB, embeddings EmbeddingClient) *VectorOperations {
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

// Close releases the resources held by the vector layer, currently
// the bleve keyword index. Bleve's background writer keeps the index
// directory alive until it is closed, so a caller that wipes the
// store must close it first.
func (vo *VectorOperations) Close() error {
	if vo == nil {
		return nil
	}
	return vo.searchIndex.Close()
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

	// Per-document lock (audit finding 4). Two concurrent
	// ingest calls for the same doc.ID would otherwise race
	// the DocumentNeedsUpdate / DeleteDocument / AddChunksBatch
	// triplet; deterministic chunk IDs save the final state
	// but the interleaving produces confusing side effects.
	// The lock is released on every return path via defer.
	docMu := vo.lockForDoc(doc.ID)
	docMu.Lock()
	defer docMu.Unlock()

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
		// Mirror the parent document's file path so search
		// results can show a friendly filename when the doc
		// has no title. Empty doc.FilePath (e.g. web upload
		// that the parser never tied to a disk location) is
		// left alone — DisplayLabel falls through to the
		// document_id in that case.
		if chunks[i].DocumentFilePath == "" {
			chunks[i].DocumentFilePath = doc.FilePath
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
//
// The tag and category filters are applied in Go after the retrieval
// — see FilterByTagsAndCategories for why they cannot be pushed into
// chromem's Where filter.
func (vo *VectorOperations) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]models.SearchResult, error) {
	if query == "" {
		return nil, models.ErrSearchFailed
	}

	// Generate query embedding. EmbedQuery (not
	// GenerateEmbedding) is the dedicated query-side
	// entry point — it uses the queryPrompt instead of
	// the docPrompt, which matters for models like
	// embedding-gemma where the two prompts differ.
	embedding, err := vo.embeddings.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search vector DB
	results, err := vo.db.Search(ctx, embedding, semanticFetchSize(limit, filters), filters.ToWhere())
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	results = FilterByTagsAndCategories(results, filters)
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// GetDocumentChunks retrieves all chunks for a document.
func (vo *VectorOperations) GetDocumentChunks(ctx context.Context, documentID string) ([]*models.Chunk, error) {
	return vo.db.GetChunksByDocument(ctx, documentID)
}

// RelatedDocument is one entry in a related-documents answer: the
// UID of a document that resembles the queried one, its title, and
// the similarity score of its best matching chunk.
type RelatedDocument struct {
	UID   string  `json:"uid"`
	Title string  `json:"title,omitempty"`
	Score float64 `json:"score"`
}

// relatedCandidateFactor is how many chunk hits are fetched per
// requested document. A document contributes one hit per chunk, so
// a handful of documents can occupy the top of the search results;
// the multiplier is what gives the grouping enough candidates to
// find the requested number of distinct documents.
const relatedCandidateFactor = 10

// relatedCandidateCap bounds the candidate search so a huge
// collection cannot turn one request into a full scan.
const relatedCandidateCap = 200

// RelatedDocuments returns the documents most similar to the
// document with the given UID.
//
// The query vector is the centroid of the document's own chunk
// embeddings — the document as a whole, with no call to the
// embeddings server. Candidates come from a vector search and are
// then grouped by document: a document's score is its best chunk
// hit. The queried document is never in the answer.
//
// Returns ErrNotFound when no document with that UID has been
// ingested.
func (vo *VectorOperations) RelatedDocuments(ctx context.Context, uid string, limit int) ([]RelatedDocument, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}

	chunks, err := vo.db.GetChunksByDocument(ctx, uid)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, ErrNotFound
	}

	embeddings, err := vo.db.EmbeddingsForChunks(ctx, chunks)
	if err != nil {
		return nil, err
	}
	centroid, err := centroidEmbedding(embeddings)
	if err != nil {
		return nil, err
	}

	candidates := min(limit*relatedCandidateFactor, relatedCandidateCap)
	hits, err := vo.db.Search(ctx, centroid, candidates, nil)
	if err != nil {
		return nil, err
	}

	// Group the chunk hits by document: the best hit wins.
	best := make(map[string]RelatedDocument, len(hits))
	for _, hit := range hits {
		if hit.DocumentID == "" || hit.DocumentID == uid {
			continue
		}
		current, seen := best[hit.DocumentID]
		if seen && current.Score >= float64(hit.Similarity) {
			continue
		}
		best[hit.DocumentID] = RelatedDocument{
			UID:   hit.UID,
			Title: hit.DocumentTitle,
			Score: float64(hit.Similarity),
		}
	}

	out := make([]RelatedDocument, 0, len(best))
	for _, doc := range best {
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].UID < out[j].UID
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// centroidEmbedding averages the given vectors into one query
// vector. The result is not unit length — the vector store
// normalizes the query before the cosine comparison, so there is
// no need to normalize here.
func centroidEmbedding(embeddings [][]float32) ([]float32, error) {
	if len(embeddings) == 0 {
		return nil, ErrNotFound
	}
	dim := len(embeddings[0])
	if dim == 0 {
		return nil, errors.New("stored embedding has no dimensions")
	}

	centroid := make([]float32, dim)
	for _, vec := range embeddings {
		if len(vec) != dim {
			return nil, errors.New("stored embeddings have mixed dimensions")
		}
		for i, v := range vec {
			centroid[i] += v
		}
	}
	for i := range centroid {
		centroid[i] /= float32(len(embeddings))
	}
	return centroid, nil
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

// DocumentFingerprint returns the stored fingerprint (and the
// last-ingested timestamp) for the document with the given UID,
// or (zero, false, nil) when no document with that UID has been
// ingested. The bool is the canonical "exists" signal.
//
// Cheap: a single chromem query against the document_id metadata
// filter using a dummy embedding. Safe to call from ingest
// preflight flows on every batch ingest.
func (vo *VectorOperations) DocumentFingerprint(ctx context.Context, documentID string) (DocumentFingerprintInfo, bool, error) {
	return vo.db.DocumentFingerprint(ctx, documentID)
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

// UpdateDocumentMetadata patches tags/categories/URLs on
// every chunk belonging to patch.DocumentID. Tags merge
// or replace based on patch.MergeTags; categories and
// URLs always replace wholesale. Returns models.ErrNotFound
// when no chunks match (operator got the document_id
// wrong). Embeddings are not re-computed — this is a
// metadata-only update.
//
// The keyword index is re-indexed with the patched chunks so
// both stores agree on the document's tags and categories.
// bleve replaces a document by ID, so re-indexing is enough —
// no delete step is needed.
func (vo *VectorOperations) UpdateDocumentMetadata(ctx context.Context, patch DocumentMetadataPatch) error {
	if err := vo.db.UpdateDocumentMetadata(ctx, patch); err != nil {
		return err
	}
	if vo.searchIndex == nil {
		return nil
	}
	chunks, err := vo.db.GetChunksByDocument(ctx, patch.DocumentID)
	if err != nil {
		return fmt.Errorf("read chunks for keyword reindex: %w", err)
	}
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
	if err := vo.searchIndex.AddBatch(summaries); err != nil {
		return fmt.Errorf("re-index chunks for keyword search: %w", err)
	}
	return nil
}

// GetUniqueDocumentsPaged returns a page of unique
// documents plus the total corpus size. limit=0 means
// "no limit" (return everything). See VectorDB for the
// full rationale on why the chunk walk is unavoidable.
func (vo *VectorOperations) GetUniqueDocumentsPaged(ctx context.Context, limit, offset int) ([]models.DocumentInfo, int, error) {
	return vo.db.GetUniqueDocumentsPaged(ctx, limit, offset)
}
