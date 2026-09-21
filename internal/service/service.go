package service

import (
	"fmt"
	"log"
	"strings"

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
	db, err := vector.NewVectorDB(
		cfg.VectorDB.CollectionName,
		cfg.VectorDB.EmbeddingDimension,
		cfg.VectorDB.PersistenceDir,
		cfg.Ollama.EmbeddingModel,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize vector DB: %w", err)
	}

	embeddings := vector.NewOpenAIEmbeddingClientWithOptions(
		cfg.Ollama.BaseURL,
		cfg.Ollama.EmbeddingModel,
		cfg.Ollama.EffectiveEmbeddingAPIKey(),
		cfg.Ollama.Timeout,
		cfg.Ollama.EmbeddingDimensions,
		cfg.Ollama.EmbeddingConcurrency,
		cfg.Ollama.EmbeddingDocPrompt,
		cfg.Ollama.EmbeddingQueryPrompt,
	)

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
		config:       cfg,
		parser:       docParser,
		chunker:      docChunker,
		vectorOps:    vectorOps,
		llmClient:    newLLMChatClient(cfg),
		cache:        querycache.New[[]models.SearchResult](cacheSize, cacheTTL),
		chatSessions: NewChatSessionStore(ChatSessionStoreConfig{MaxTurns: cfg.Ragabast.ChatSessionMaxTurns}),
		embedModel:   cfg.Ollama.EmbeddingModel,
	}, nil
}
