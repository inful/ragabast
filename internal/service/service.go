package service

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ragabast/internal/chunker"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/parser"
	"github.com/ragabast/internal/service/querycache"
	"github.com/ragabast/internal/vector"
)

// Service orchestrates the RAG system operations. Its methods are
// split across ingest.go (writes), query.go (LLM-backed reads),
// documents.go (document and chunk CRUD), catalog.go (tags and
// categories), chat_session.go (chat history), and stats.go
// (health and counters); this file only holds the type and its
// constructor so the wiring is visible at a glance.
//
// cache holds the bounded LRU query cache (issue #13). nil means
// "cache disabled" — Search/HybridSearch run every time without
// touching a cache. Clear() is invoked from the write paths
// (IngestDocument, DeleteDocument, DeleteChunk) because any
// state change could shift query result rankings.
//
// chatSessions holds the in-memory chat history store (issue
// #22). Nil means "history disabled" — every /chat/message
// request is one-shot, with no follow-up context threaded
// into the LLM call. The chat handler still works without a
// session store; it's just stateless.
type Service struct {
	config       *config.Config
	parser       *parser.DocbuilderParser
	chunker      *chunker.Chunker
	vectorOps    *vector.VectorOperations
	llmClient    llmChatClient
	cache        *querycache.Cache[[]models.SearchResult]
	chatSessions *ChatSessionStore
	embedModel   string // captured at construction so the cache key stays stable
	// embedQueryPrompt is the query-side task prefix, captured for
	// the same reason as embedModel: it changes how the query is
	// embedded, so it belongs in the cache key.
	embedQueryPrompt string

	// now is the reference clock for Hugo "don't publish"
	// preflight checks (issue #97). Defaults to time.Now in
	// production; tests swap it for a fixed value via
	// setServiceNow.
	now func() time.Time
}

// buildDocbuilderURL returns the synthetic permalink for a
// document with the given UID, of the form `<base>/_uid/<uid>/`.
// Returns "" when ragabast.docbuilder_base_url is not configured
// or when the document has no UID — either case means there is
// nothing sensible to link to.
//
// docbuilder's /_uid/<uid>/ alias is derived from the
// frontmatter UID (which never changes) rather than the file
// path. Downstream systems, indexers, bookmarks, and external
// links can rely on this URL staying stable even when the doc's
// file moves on disk. Using the bare UID (e.g.
// `<base>/<uid>`) would collide with file-path-based URLs and
// break on rename — so the /_uid/ segment is mandatory.
//
// Slash handling: the base URL is assumed to be well-formed;
// trailing slashes on the base and leading slashes on the UID
// are tolerated. The result always has exactly one slash
// between segments.
func (s *Service) buildDocbuilderURL(uid string) string {
	base := strings.TrimRight(s.config.Ragabast.DocbuilderBaseURL, "/")
	if base == "" || uid == "" {
		return ""
	}
	return base + "/_uid/" + strings.TrimLeft(uid, "/") + "/"
}

// NewService creates a new service instance.
func NewService(cfg *config.Config) (*Service, error) {
	db, err := openVectorDB(cfg)
	if err != nil {
		return nil, err
	}

	embeddings, err := vector.NewEmbeddingClientFromOptions(vector.EmbeddingClientOptions{
		Provider: vector.EmbeddingProvider(cfg.EmbeddingProvider),

		OpenAIBaseURL:     cfg.Ollama.BaseURL,
		OpenAIModel:       cfg.Ollama.EmbeddingModel,
		OpenAIAPIKey:      cfg.Ollama.EffectiveEmbeddingAPIKey(),
		OpenAITimeout:     cfg.Ollama.Timeout,
		OpenAIDimensions:  cfg.Ollama.EmbeddingDimensions,
		OpenAIConcurrency: cfg.Ollama.EmbeddingConcurrency,
		OpenAIDocPrompt:   cfg.Ollama.EmbeddingDocPrompt,
		OpenAIQueryPrompt: cfg.Ollama.EmbeddingQueryPrompt,

		EmbeddingGemmaBaseURL:     cfg.EmbeddingGemma.BaseURL,
		EmbeddingGemmaTimeout:     cfg.EmbeddingGemma.Timeout,
		EmbeddingGemmaDimensions:  cfg.EmbeddingGemma.EmbeddingDimensions,
		EmbeddingGemmaConcurrency: cfg.EmbeddingGemma.EmbeddingConcurrency,
		EmbeddingGemmaDocPrompt:   cfg.EmbeddingGemma.EmbeddingDocPrompt,
		EmbeddingGemmaQueryPrompt: cfg.EmbeddingGemma.EmbeddingQueryPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize embeddings client: %w", err)
	}

	vectorOps := vector.NewVectorOperations(db, embeddings)

	// Wire the bleve-backed keyword index alongside the
	// vector DB. The keyword index defaults to the sibling
	// path <PersistenceDir>/search, but operators can
	// override with vectordb.keyword_index_dir to put it on
	// local disk while keeping the vector DB on NFS or a
	// shared mount.
	searchDir := vector.ResolveKeywordIndexPath(
		cfg.VectorDB.PersistenceDir,
		cfg.VectorDB.KeywordIndexDir,
	)
	if cfg.VectorDB.KeywordIndexDir != "" {
		log.Printf("service: keyword index on %s (overriding default <%s>/search layout)",
			cfg.VectorDB.KeywordIndexDir, cfg.VectorDB.PersistenceDir)
	}
	searchIdx, err := vector.LoadSearchIndex(searchDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize search index: %w", err)
	}
	vectorOps.SetSearchIndex(searchIdx)

	docParser := parser.NewDocbuilderParser()
	docChunker := chunker.NewChunker(cfg.Processing.MaxChunkSize, cfg.Processing.MinChunkSize, cfg.Processing.ChunkOverlap)

	// Query cache wiring (issue #13). size == 0 disables the
	// cache (every call falls through). The embedModel is
	// captured at construction time so the cache key stays
	// stable across the service's lifetime — operators
	// swapping embedding models will invalidate entries
	// implicitly because the next Get computes a new key.
	cacheSize := cfg.Ragabast.QueryCacheSize
	cacheTTL := cfg.Ragabast.QueryCacheTTL

	return &Service{
		config:    cfg,
		parser:    docParser,
		chunker:   docChunker,
		vectorOps: vectorOps,
		llmClient: newLLMChatClient(cfg),
		cache:     querycache.New[[]models.SearchResult](cacheSize, cacheTTL),
		chatSessions: NewChatSessionStore(ChatSessionStoreConfig{
			MaxTurns: cfg.Ragabast.ChatSessionMaxTurns,
			IdleTTL:  cfg.Ragabast.ChatSessionIdleTTL,
		}),
		embedModel:       cfg.EffectiveEmbeddingModel(),
		embedQueryPrompt: cfg.EffectiveEmbeddingQueryPrompt(),
		now:              time.Now,
	}, nil
}

// openVectorDB opens the vector store, invalidating a stale corpus
// when the embedder changed.
//
// The store on disk records the embedder that wrote it (the model
// marker). When that name differs from the configured one, the
// stored vectors live in the old model's vector space and cannot
// answer a search under the new one, so the corpus has to be
// re-embedded. Rather than leaving the operator with a corpus that
// silently returns nonsense, the stale store is invalidated here:
// it is moved aside (not deleted — a mistyped model name stays
// recoverable) and the server starts with an empty store.
//
// vectordb.fail_on_model_mismatch turns the automatic invalidation
// off: the server then refuses to start and names
// `ragabast vector reset --force` as the fix.
func openVectorDB(cfg *config.Config) (*vector.VectorDB, error) {
	db, err := vector.NewVectorDBWithOptions(vector.Options{
		Name:                cfg.VectorDB.CollectionName,
		EmbeddingDimension:  cfg.VectorDB.EmbeddingDimension,
		PersistenceDir:      cfg.VectorDB.PersistenceDir,
		EmbeddingModel:      cfg.EffectiveEmbeddingModel(),
		EmbeddingDocPrompt:  cfg.EffectiveEmbeddingDocPrompt(),
		FailOnModelMismatch: true,
	})
	if err == nil {
		return db, nil
	}

	var mismatch *vector.ModelMismatchError
	if !errors.As(err, &mismatch) || cfg.VectorDB.FailOnModelMismatch {
		return nil, fmt.Errorf("failed to initialize vector DB: %w", err)
	}

	stale, qErr := vector.QuarantineStore(cfg.VectorDB.PersistenceDir, cfg.VectorDB.KeywordIndexDir)
	if qErr != nil {
		return nil, fmt.Errorf("failed to invalidate the store written by %q: %w", mismatch.Persisted, qErr)
	}
	logInvalidatedStore(mismatch)
	logStaleStorePath(stale)
	log.Printf("service: re-ingest your documents (ragabast ingest, or let docbuilder re-push them) to rebuild the index")

	db, err = vector.NewVectorDBWithOptions(vector.Options{
		Name:                cfg.VectorDB.CollectionName,
		EmbeddingDimension:  cfg.VectorDB.EmbeddingDimension,
		PersistenceDir:      cfg.VectorDB.PersistenceDir,
		EmbeddingModel:      cfg.EffectiveEmbeddingModel(),
		EmbeddingDocPrompt:  cfg.EffectiveEmbeddingDocPrompt(),
		FailOnModelMismatch: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize vector DB after invalidating the stale store: %w", err)
	}
	return db, nil
}

// logInvalidatedStore tells the operator why the corpus was
// invalidated: the embedder changed, or the doc prompt did.
func logInvalidatedStore(mismatch *vector.ModelMismatchError) {
	if mismatch.Reason == vector.ReasonDocPrompt {
		log.Printf("service: the vector store was embedded with doc prompt %q but the configured doc prompt is %q. "+
			"Vectors from the old prompt cannot be searched under the new one, so they were invalidated",
			mismatch.PersistedDocPrompt, mismatch.ConfiguredDocPrompt)
		return
	}
	log.Printf("service: the vector store was embedded with %q but the configured embedder is %q. "+
		"The old vectors cannot be searched under the new embedder, so they were invalidated",
		mismatch.Persisted, mismatch.Configured)
}

// logStaleStorePath tells the operator where the invalidated store
// went, so it can be inspected or deleted.
func logStaleStorePath(stale string) {
	if stale == "" {
		return
	}
	log.Printf("service: the previous store was moved to %s. Delete it once the corpus has been re-ingested", stale)
}

// Close releases the resources the service holds, currently the
// bleve keyword index behind the vector layer. Bleve keeps a
// background writer alive until the index is closed, so a caller
// that wipes the store (a reset, a test's temp dir) must close the
// service first.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	return s.vectorOps.Close()
}

// SetNow replaces the service's clock for tests that need a
// deterministic reference time (issue #97 — the unpublished
// preflight is anchored to "now"). Production callers never call
// this; the constructor sets time.Now as the default.
func (s *Service) SetNow(now func() time.Time) {
	s.now = now
}

// StopChatSessionsGC stops the ChatSessionStore's idle-TTL
// GC goroutine. Safe to call when the store is nil (no-op)
// or when IdleTTL was 0 (the goroutine isn't running). Idempotent.
//
// The web server's Stop() calls this so shutdown is clean;
// the GC exits in well under a second after Stop is called.
func (s *Service) StopChatSessionsGC() {
	if s == nil || s.chatSessions == nil {
		return
	}
	s.chatSessions.Stop()
}
