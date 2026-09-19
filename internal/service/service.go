package service

import (
	"fmt"

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
