package service

import (
	"fmt"
	"strings"

	"github.com/ragabast/internal/chunker"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/parser"
	"github.com/ragabast/internal/vector"
)

// Service orchestrates the RAG system operations. Its methods are
// split across ingest.go (writes), query.go (LLM-backed reads),
// documents.go (document and chunk CRUD), catalog.go (tags and
// categories), and stats.go (health and counters); this file only
// holds the type and its constructor so the wiring is visible at
// a glance.
type Service struct {
	config    *config.Config
	parser    *parser.DocbuilderParser
	chunker   *chunker.Chunker
	vectorOps *vector.VectorOperations
	llmClient llmChatClient
}

// buildDocbuilderURL returns the synthetic permalink for a
// document with the given UID, of the form `<base>/<uid>`.
// Returns "" when ragabast.docbuilder_base_url is not configured
// or when the document has no UID — either case means there is
// nothing sensible to link to.
//
// Slash handling: the base URL is assumed to be well-formed;
// trailing slashes on the base and leading slashes on the UID
// are tolerated. The result always has exactly one slash
// between base and UID.
func (s *Service) buildDocbuilderURL(uid string) string {
	base := strings.TrimRight(s.config.Ragabast.DocbuilderBaseURL, "/")
	if base == "" || uid == "" {
		return ""
	}
	return base + "/" + strings.TrimLeft(uid, "/")
}

// NewService creates a new service instance.
func NewService(cfg *config.Config) (*Service, error) {
	db, err := vector.NewVectorDB(cfg.VectorDB.CollectionName, cfg.VectorDB.EmbeddingDimension, cfg.VectorDB.PersistenceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize vector DB: %w", err)
	}

	embeddings := vector.NewOpenAIEmbeddingClientWithOptions(
		cfg.Ollama.BaseURL,
		cfg.Ollama.EmbeddingModel,
		cfg.Ollama.EffectiveEmbeddingAPIKey(),
		cfg.Ollama.Timeout,
		cfg.Ollama.EmbeddingDimensions,
	)

	vectorOps := vector.NewVectorOperations(db, embeddings)

	docParser := parser.NewDocbuilderParser()
	docChunker := chunker.NewChunker(cfg.Processing.MaxChunkSize, cfg.Processing.MinChunkSize, cfg.Processing.ChunkOverlap)

	return &Service{
		config:    cfg,
		parser:    docParser,
		chunker:   docChunker,
		vectorOps: vectorOps,
		llmClient: newLLMChatClient(cfg),
	}, nil
}
