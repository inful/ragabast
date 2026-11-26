package vector

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	embeddingModel     string
	embeddingDocPrompt string
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

	// EmbeddingDocPrompt is the doc-side task prefix the corpus
	// was embedded with. It is part of the store's identity: the
	// vectors it produces sit in a different place than the ones
	// an empty (or different) prompt produces, so a prompt change
	// invalidates the corpus the same way a model change does.
	EmbeddingDocPrompt string

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
// embedding model — and which doc prompt — produced the vectors in
// this persistence directory. Read on every constructor call;
// written on the first successful AddChunk / AddChunksBatch.
//
// Lives at the persistence root (next to chromem-go's own
// files) so a single ls of the directory answers the
// "what model is this?" question.
//
// Format: the model name on the first line, then optional
// `key=value` lines. A marker written before the doc prompt was
// tracked carries only the model name; the prompt is then
// "unknown" and the prompt check is skipped.
const modelMarkerFile = ".embedding_model"

// docPromptMarkerKey is the marker key holding the doc prompt the
// corpus was embedded with (stored quoted, so it stays on one line).
const docPromptMarkerKey = "doc_prompt"

// ErrModelMismatch is returned (wrapped) by NewVectorDB when
// the persisted marker carries a different model name than the
// configured one and FailOnModelMismatch is true. Callers
// can errors.Is(err, ErrModelMismatch) to detect.
var ErrModelMismatch = errors.New("vector DB embedding model mismatch")

// NewVectorDB creates a new vector database instance. It is
// shorthand for NewVectorDBWithOptions with FailOnModelMismatch
// set to true (the safe default).
//
// The persistence directory, when non-empty, must contain vectors
// from EmbeddingModel. A model swap requires wiping the directory
// (`ragabast vector reset --force`) or setting
// FailOnModelMismatch=false in the Options form.
//
// The doc prompt is not part of this shorthand: callers that care
// about prompt identity (the server, the doctor) use the Options
// form and pass EmbeddingDocPrompt.
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
		// Ensure the directory exists AND the binary can write
		// to it. MkdirAll alone is insufficient: it is a no-op
		// when the target already exists, so a pre-existing
		// directory owned by another uid (the typical K8s PVC
		// scenario when fsGroup is not configured) passes
		// MkdirAll silently. The probe file surfaces the real
		// problem at startup, before chromem-go's first index
		// write fails several layers deep in its state machine.
		if err := ensureWritableDir(opts.PersistenceDir); err != nil {
			return nil, fmt.Errorf("persistence directory %q: %w", opts.PersistenceDir, err)
		}

		// Check the model marker before opening chromem-go.
		// We do this first so a mismatch returns the cleanest
		// error path (no chromem-go state to clean up).
		if err := checkModelMarker(opts.PersistenceDir, opts.EmbeddingModel, opts.EmbeddingDocPrompt, opts.FailOnModelMismatch); err != nil {
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
		embeddingDocPrompt: opts.EmbeddingDocPrompt,
		persistenceDir:     opts.PersistenceDir,
	}, nil
}

// ModelMismatchError reports that the on-disk store was written by
// a different embedding model — or with a different doc prompt —
// than the one now configured. The vectors live in the old
// embedder's vector space, so they cannot be searched under the
// new one — the corpus has to be re-embedded.
//
// It wraps ErrModelMismatch, so `errors.Is(err, ErrModelMismatch)`
// keeps working for callers that only care that a mismatch
// happened; the typed fields let a caller name both sides without
// parsing the message.
type ModelMismatchError struct {
	// Persisted is the model name recorded in the store's marker.
	Persisted string
	// Configured is the model name the store is being opened with.
	Configured string
	// PersistedDocPrompt is the doc prompt the corpus was embedded
	// with. Empty with a marker that predates prompt tracking
	// means "unknown" — the prompt check is skipped then.
	PersistedDocPrompt string
	// ConfiguredDocPrompt is the doc prompt the store is being
	// opened with.
	ConfiguredDocPrompt string
	// Reason names what did not match: "model" or "doc prompt".
	Reason string
}

func (e *ModelMismatchError) Error() string {
	if e.Reason == docPromptMarkerKey {
		return fmt.Sprintf(
			"the vector store was embedded with doc prompt %q but the configured doc prompt is %q; "+
				"vectors from the old prompt cannot be searched safely under the new one",
			e.PersistedDocPrompt, e.ConfiguredDocPrompt)
	}
	return fmt.Sprintf(
		"persisted embedding model %q differs from configured %q; "+
			"vectors from %q cannot be searched safely under %q",
		e.Persisted, e.Configured, e.Persisted, e.Configured)
}

// Is makes errors.Is(err, ErrModelMismatch) true.
func (e *ModelMismatchError) Is(target error) bool { return target == ErrModelMismatch }

// QuarantineStore moves an on-disk store aside so a fresh one can be
// created in its place, and returns where the stale copy went.
//
// Quarantine, not delete: the stale vectors are useless under a new
// embedder, but an operator who mistyped a model name should still
// be able to get their corpus back. The caller is expected to log
// loudly and tell the operator the stale copy can be deleted.
//
// The keyword index normally lives inside the persistence dir
// (<persistence_dir>/search) and moves with it. A separately
// configured keyword index dir (the documented NFS setup) is moved
// aside too — leaving it in place would keep chunks whose vectors
// are gone.
func QuarantineStore(persistenceDir, keywordIndexDir string) (string, error) {
	if persistenceDir == "" {
		return "", errors.New("persistence dir is empty; refusing to quarantine")
	}

	stamp := time.Now().Format("20060102-150405")
	target := persistenceDir + ".stale-" + stamp

	if err := os.Rename(persistenceDir, target); err != nil {
		if os.IsNotExist(err) {
			// Nothing on disk — nothing to invalidate.
			return "", nil
		}
		return "", fmt.Errorf("failed to move %s aside: %w", persistenceDir, err)
	}

	kwDir := ResolveKeywordIndexPath("", keywordIndexDir)
	if kwDir != "" && !strings.HasPrefix(kwDir, persistenceDir+string(os.PathSeparator)) {
		if err := os.Rename(kwDir, kwDir+".stale-"+stamp); err != nil && !os.IsNotExist(err) {
			return target, fmt.Errorf("failed to move keyword index %s aside: %w", kwDir, err)
		}
	}

	// Recreate the empty dir so the fresh store can open without
	// the chromem-go "no such directory" error.
	if err := os.MkdirAll(persistenceDir, 0o755); err != nil {
		return target, fmt.Errorf("failed to recreate %s: %w", persistenceDir, err)
	}
	return target, nil
}

// checkModelMarker reads the on-disk marker file at persistDir
// and compares its contents to the configured model and doc
// prompt. Behavior:
//
//   - Marker missing: nothing to check. Return nil. The first
//     AddChunk call will write the marker.
//   - configuredModel empty ("unknown"): nothing to compare
//     against. Return nil without touching the marker. An
//     operator who never declared a model name (the /v2/embed
//     path has no model field on the wire) must not be blocked
//     by a marker written under a different provider's name.
//   - Marker present, model matches, doc prompt matches (or the
//     marker predates prompt tracking and the prompt is
//     therefore unknown): return nil.
//   - Marker present, model differs: error (or warning) naming
//     both models.
//   - Marker present, model matches but the doc prompt differs:
//     error (or warning) naming both prompts. The vectors were
//     produced under the old prompt, so they are not comparable
//     with vectors produced under the new one.
//
// The doc-prompt check is skipped when the marker carries no
// prompt at all: a store written before the prompt was tracked
// cannot be judged, and refusing to open it would strand every
// existing install. The warning tells the operator what to do
// instead.
func checkModelMarker(persistDir, configuredModel, configuredDocPrompt string, failOnMismatch bool) error {
	if configuredModel == "" {
		return nil
	}
	path := filepath.Join(persistDir, modelMarkerFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read model marker at %s: %w", path, err)
	}

	persistedModel, persistedDocPrompt, hasDocPrompt := parseModelMarker(data)
	if persistedModel != configuredModel {
		return reportMismatch(&ModelMismatchError{
			Persisted:  persistedModel,
			Configured: configuredModel,
			Reason:     "model",
		}, failOnMismatch)
	}

	if !hasDocPrompt {
		log.Printf("⚠ the vector store at %s predates doc-prompt tracking; if you change the doc prompt, "+
			"re-embed the corpus (`ragabast vector reset --force` then `ragabast ingest`) so every chunk "+
			"carries the same prefix", persistDir)
		return nil
	}
	if persistedDocPrompt != configuredDocPrompt {
		return reportMismatch(&ModelMismatchError{
			Persisted:           persistedModel,
			Configured:          configuredModel,
			Reason:              docPromptMarkerKey,
			PersistedDocPrompt:  persistedDocPrompt,
			ConfiguredDocPrompt: configuredDocPrompt,
		}, failOnMismatch)
	}
	return nil
}

// reportMismatch returns the mismatch error, or logs it and
// returns nil when the operator asked for the lenient mode.
func reportMismatch(mismatch *ModelMismatchError, failOnMismatch bool) error {
	if failOnMismatch {
		return fmt.Errorf("%w; recover with `ragabast vector reset --force` "+
			"then `ragabast ingest` to rebuild from source documents", mismatch)
	}
	log.Printf("⚠ %s; proceeding because FailOnModelMismatch=false", mismatch.Error())
	return nil
}

// parseModelMarker reads the marker file: the model name on the
// first line, then `key=value` lines. The doc prompt is stored
// quoted so a prompt containing a newline stays on one line.
//
// A marker written before the doc prompt was tracked carries only
// the model name; hasDocPrompt comes back false ("unknown") and
// the prompt check is skipped.
func parseModelMarker(data []byte) (model string, docPrompt string, hasDocPrompt bool) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	model = strings.TrimSpace(lines[0])
	for _, line := range lines[1:] {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), docPromptMarkerKey+"=")
		if !ok {
			continue
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			continue
		}
		docPrompt = unquoted
		hasDocPrompt = true
	}
	return model, docPrompt, hasDocPrompt
}

// writeModelMarkerIfMissing stamps the embedding model — and the
// doc prompt the corpus was embedded with — on the persistence
// directory the first time data is written.
//
// Idempotent: subsequent calls observe the existing file and
// do not re-write. The marker is only written when a chunk
// successfully lands in the collection — a failed write
// must not commit a model claim on data that isn't there.
//
// Not re-writing is deliberate: the marker records the embedder
// configuration the CORPUS was built with. Re-stamping it on every
// ingest would make a prompt change invisible — the first ingest
// after the change would overwrite the fingerprint before anyone
// looked.
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
	data := []byte(db.embeddingModel + "\n" +
		docPromptMarkerKey + "=" + strconv.Quote(db.embeddingDocPrompt) + "\n")
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

// DocumentMetadataPatch captures which metadata fields
// to update on a document's chunks. A nil field means
// "don't touch"; an empty slice means "clear". Tags are
// handled separately (merge vs replace) at the service
// layer because the semantics are operation-specific.
type DocumentMetadataPatch struct {
	DocumentID string
	Tags       []string // nil = no change; otherwise the post-merge value
	Categories []string // nil = no change; non-nil = replace
	URLs       []string // nil = no change; non-nil = replace
	MergeTags  bool     // when Tags != nil: true = merge into existing, false = replace
}

// UpdateDocumentMetadata patches the chunk metadata
// (tags, categories, urls) for every chunk belonging to
// documentID. Tags merge/replace semantics are handled
// here based on patch.MergeTags. Categories and URLs are
// replaced wholesale when patch specifies them — they're
// always an explicit operator declaration.
//
// The implementation walks GetChunksByDocument, mutates
// each chunk's metadata, and re-adds them with the SAME chunk
// IDs and embeddings + the new metadata. Embeddings are not
// re-computed (metadata-only update). The chunk_id is
// stable across the update.
//
// No delete step is needed: chromem-go replaces a stored
// document by ID, so re-adding the chunks overwrites them in
// place. That also means there is no window in which the
// document is missing from the store.
//
// Returns models.ErrNotFound when no chunks match
// documentID so callers can surface "no such document"
// without inspecting an empty result. The caller does
// the existence check; this method doesn't have a
// separate "exists" call so we save a chunk scan.
func (db *VectorDB) UpdateDocumentMetadata(ctx context.Context, patch DocumentMetadataPatch) error {
	if patch.DocumentID == "" {
		return fmt.Errorf("%w: empty document_id", models.ErrInvalidInput)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	chunks, err := db.getChunksByDocumentLocked(ctx, patch.DocumentID)
	if err != nil {
		return fmt.Errorf("read chunks for update: %w", err)
	}
	if len(chunks) == 0 {
		return models.ErrNotFound
	}

	// Re-derive each chunk's metadata with the patch
	// applied. We don't store the original embeddings on
	// the chunks themselves — they're in chromem-go's
	// collection — so we need to fetch the embeddings
	// back from chromem-go. Do this by re-walking the
	// same query we used to get the chunks.
	embeddings, err := db.embeddingsForChunksLocked(ctx, chunks)
	if err != nil {
		return fmt.Errorf("fetch embeddings for update: %w", err)
	}

	// Apply the patch to each chunk's metadata.
	for _, chunk := range chunks {
		applyMetadataPatch(chunk, patch)
	}

	// Re-add with the SAME IDs + same embeddings + updated
	// metadata. chromem-go replaces by ID, so the metadata
	// update lands in place; the embeddings are unchanged so
	// the similarity scores stay identical.
	//
	// The locked variant is used because this method already
	// holds db.mu — the exported AddChunksBatch takes the
	// same (non-reentrant) lock itself.
	if err := db.addChunksBatchLocked(ctx, chunks, embeddings); err != nil {
		return fmt.Errorf("re-add with new metadata: %w", err)
	}
	return nil
}

// getChunksByDocumentLocked is the lock-free variant of
// GetChunksByDocument — the caller must already hold
// db.mu (write lock). UpdateDocumentMetadata uses it to
// avoid re-locking between the read and the re-write.
func (db *VectorDB) getChunksByDocumentLocked(ctx context.Context, documentID string) ([]*models.Chunk, error) {
	count := db.collection.Count()
	if count == 0 {
		return []*models.Chunk{}, nil
	}
	dummyEmbedding := make([]float32, db.embeddingDimension)
	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       count,
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

// embeddingsForChunksLocked runs the same query as
// getChunksByDocumentLocked but only to extract the
// embedding vector from each result. Used by
// UpdateDocumentMetadata so the embeddings survive the
// re-add.
func (db *VectorDB) embeddingsForChunksLocked(ctx context.Context, chunks []*models.Chunk) ([][]float32, error) {
	count := db.collection.Count()
	if count == 0 {
		return nil, nil
	}
	// Build a where filter that matches the chunks we
	// care about — same shape as getChunksByDocumentLocked
	// but parameterised so this can also work for bulk
	// updates across many documents (future expansion).
	dummyEmbedding := make([]float32, db.embeddingDimension)
	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       count,
		Where:          map[string]string{"document_id": chunks[0].DocumentID},
	}
	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return nil, err
	}
	// Map by chunk ID so we don't rely on result order.
	byID := make(map[string][]float32, len(results))
	for _, r := range results {
		id := r.Metadata["chunk_id"]
		if id != "" {
			byID[id] = r.Embedding
		}
	}
	out := make([][]float32, len(chunks))
	for i, chunk := range chunks {
		emb, ok := byID[chunk.ID]
		if !ok {
			return nil, fmt.Errorf("chunk %s: embedding not found in collection", chunk.ID)
		}
		out[i] = emb
	}
	return out, nil
}

// applyMetadataPatch mutates chunk's metadata in-place
// per the patch. Tags merge-or-replace based on
// patch.MergeTags; categories and URLs always replace
// wholesale. Empty-slice fields clear the corresponding
// metadata; nil fields leave it untouched.
//
// (Helper, not exported — UpdateDocumentMetadata owns the
// merge semantics; the vector layer just persists.)
func applyMetadataPatch(chunk *models.Chunk, patch DocumentMetadataPatch) {
	if patch.Tags != nil {
		if patch.MergeTags {
			chunk.DocumentTags = mergeUniqueStrings(chunk.DocumentTags, patch.Tags)
		} else {
			chunk.DocumentTags = copySlice(patch.Tags)
		}
	}
	if patch.Categories != nil {
		chunk.DocumentCategories = copySlice(patch.Categories)
	}
	if patch.URLs != nil {
		chunk.DocumentURLs = copySlice(patch.URLs)
	}
}

func mergeUniqueStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func copySlice(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
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

	return db.addChunksBatchLocked(ctx, chunks, embeddings)
}

// addChunksBatchLocked is the lock-free variant of AddChunksBatch —
// the caller must already hold db.mu. UpdateDocumentMetadata uses it
// because db.mu is a sync.RWMutex (not reentrant): calling the
// exported AddChunksBatch from a method that already holds the write
// lock deadlocks.
func (db *VectorDB) addChunksBatchLocked(ctx context.Context, chunks []*models.Chunk, embeddings [][]float32) error {
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
			DocumentURLs:       splitNonEmptyLines(result.Metadata["document_urls"]),
			DocumentTags:       splitNonEmptyLines(result.Metadata["document_tags"]),
			DocumentCategories: splitNonEmptyLines(result.Metadata["document_categories"]),
			ParentID:           result.Metadata["parent_id"],
			Similarity:         result.Similarity,
			Fingerprint:        result.Metadata["fingerprint"],
			UID:                result.Metadata["uid"],
			DocumentFilePath:   result.Metadata["document_file_path"],
			// SourceKind carries the persisted value when the
			// chunk has one; legacy chunks (no source_kind entry)
			// get a UID-prefix backfill via inferSourceKind so the
			// citation dispatch and post-filter can branch on the
			// kind without re-deriving it themselves.
			SourceKind: inferSourceKind(result.Metadata["source_kind"], result.Metadata["uid"]),
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
		NResults:       count,
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
		NResults:       count,
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

// splitNonEmptyLines and parseRFC3339 moved to metadata.go.

// DocumentFingerprintInfo is the metadata a DocumentFingerprint
// call returns. Fingerprint is the content hash stored at
// ingest time; IngestedAt is when the chunk row was written
// (RFC3339, parsed from the document_updated_at metadata
// field). Zero IngestedAt means the metadata field was
// absent on the stored chunks — defensive only, since the
// ingest path always populates it.
type DocumentFingerprintInfo struct {
	Fingerprint string
	IngestedAt  time.Time
}

// DocumentFingerprint returns the stored fingerprint for a
// document if it exists. The bool is true when the doc is
// ingested, false when no chunks with this document_id have
// been stored — callers can use the bool rather than relying
// on an empty-fingerprint sentinel to detect "never ingested".
//
// `document_id` is the docbuilder UID; the parser pins
// doc.ID = doc.UID so a UID-keyed lookup is the natural API.
//
// Powers the preflight flow: GET /api/documents/{uid}/fingerprint
// lets a client ask "what fingerprint did you last store for
// this UID?" without sending the document body. The client
// compares its locally-computed fingerprint against this
// value and decides whether to skip or re-ingest.
func (db *VectorDB) DocumentFingerprint(ctx context.Context, documentID string) (DocumentFingerprintInfo, bool, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if strings.TrimSpace(documentID) == "" {
		return DocumentFingerprintInfo{}, false, models.ErrInvalidInput
	}

	count := db.collection.Count()
	if count == 0 {
		return DocumentFingerprintInfo{}, false, nil
	}

	dummyEmbedding := make([]float32, db.embeddingDimension)
	options := chromem.QueryOptions{
		QueryEmbedding: dummyEmbedding,
		NResults:       min(count, 1),
		Where:          map[string]string{"document_id": documentID},
	}

	results, err := db.collection.QueryWithOptions(ctx, options)
	if err != nil {
		return DocumentFingerprintInfo{}, false, fmt.Errorf("failed to query document fingerprint: %w", err)
	}
	if len(results) == 0 {
		return DocumentFingerprintInfo{}, false, nil
	}

	info := DocumentFingerprintInfo{
		Fingerprint: strings.TrimSpace(results[0].Metadata["fingerprint"]),
		IngestedAt:  parseRFC3339(results[0].Metadata["document_updated_at"]),
	}

	return info, true, nil
}

// DocumentNeedsUpdate returns whether an ingest should replace existing data.
//
// Rules:
// - If the document does not exist, it needs an insert (true, exists=false).
// - If it exists and fingerprint matches, no-op (false, exists=true).
// - If it exists and fingerprint differs, replace (true, exists=true).
func (db *VectorDB) DocumentNeedsUpdate(ctx context.Context, documentID string, fingerprint string) (bool, bool, error) {
	info, exists, err := db.DocumentFingerprint(ctx, documentID)
	if err != nil {
		return false, false, err
	}
	if !exists {
		return true, false, nil
	}
	if strings.TrimSpace(info.Fingerprint) == strings.TrimSpace(fingerprint) {
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
		if info.FilePath == "" {
			if fp := result.Metadata["document_file_path"]; fp != "" {
				info.FilePath = fp
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

// ensureWritableDir creates dir (and any missing parents) and
// verifies the running process can write to it. The probe
// file is what catches the "directory exists but is owned by
// another uid" failure mode that MkdirAll's no-op-on-exists
// semantics otherwise mask. The probe is removed before
// returning on success so the operator doesn't see noise in
// their vector DB dir.
//
// On EACCES — the failure mode operators actually hit on K8s
// when running as the distroless `nonroot` uid 65532 against
// a PVC whose root is owned by another uid — the returned
// error names the offending path, the running uid/gid, and
// the securityContext.fsGroup fix so the operator doesn't
// have to guess. The underlying fs.ErrPermission is wrapped
// for callers that want to branch on it programmatically.
func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wrapDirPermission(dir, err)
	}

	probe, err := os.CreateTemp(dir, ".ragabast-write-probe-*")
	if err != nil {
		return wrapDirPermission(dir, err)
	}
	probeName := probe.Name()
	// Best-effort cleanup; a failed Remove is non-fatal
	// because the next startup will overwrite or skip the
	// probe, but log it so operators can clean up manually
	// if it accumulates.
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probeName)
		return fmt.Errorf("persistence directory %q: probe close: %w", dir, closeErr)
	}
	if removeErr := os.Remove(probeName); removeErr != nil {
		return fmt.Errorf("persistence directory %q: probe cleanup: %w", dir, removeErr)
	}

	return nil
}

// wrapDirPermission annotates a permission-denied error from
// MkdirAll or os.CreateTemp with the deployment-footgun
// guidance the operator needs to fix it: the running uid/gid,
// and the securityContext.fsGroup fix that closes the gap on
// Kubernetes. Non-permission errors pass through unwrapped so
// the underlying message (e.g. ENOSPC, EROFS) is preserved.
//
// The "persistence" descriptor is hardcoded because the only
// caller is ensureWritableDir, which guards the chromem-go
// vector DB persistence dir specifically. Other dirs
// (keyword index, async ingest queue) currently surface
// their own errors via the standard MkdirAll path; widening
// this helper to cover them is a one-liner if the message
// starts to confuse operators.
func wrapDirPermission(dir string, err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("persistence directory %q: %w", dir, err)
	}
	return fmt.Errorf(
		"persistence directory %q is not writable by the running process (uid=%d, gid=%d): %w. "+
			"On Kubernetes, set securityContext.fsGroup to the binary's uid/gid "+
			"(the published image runs as nonroot uid 65532); on a host volume, "+
			"chown the directory to the binary's uid; or run an init container "+
			"that fixes the ownership before the main container starts",
		dir, os.Getuid(), os.Getgid(), err)
}

// Close cleans up resources.
func (db *VectorDB) Close() error {
	// chromem-go handles cleanup automatically
	return nil
}
