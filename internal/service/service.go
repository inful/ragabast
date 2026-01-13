package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ragabast/internal/chunker"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/parser"
	"github.com/ragabast/internal/vector"
)

// Service orchestrates the RAG system operations.
type Service struct {
	config    *config.Config
	parser    *parser.DocubilderParser
	chunker   *chunker.Chunker
	vectorOps *vector.VectorOperations
}

// NewService creates a new service instance.
func NewService(cfg *config.Config) (*Service, error) {
	// Initialize vector database
	db, err := vector.NewVectorDB(cfg.VectorDB.CollectionName, cfg.VectorDB.EmbeddingDimension, cfg.VectorDB.PersistenceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize vector DB: %w", err)
	}

	// Initialize embedding client
	embeddings := vector.NewOllamaEmbeddingClientWithTimeout(cfg.Ollama.BaseURL, cfg.Ollama.EmbeddingModel, cfg.Ollama.Timeout)

	// Initialize vector operations
	vectorOps := vector.NewVectorOperations(db, embeddings)

	// Initialize parser and chunker
	docParser := parser.NewDocubilderParser()
	chunker := chunker.NewChunker(cfg.Processing.MaxChunkSize, cfg.Processing.MinChunkSize, cfg.Processing.ChunkOverlap)

	return &Service{
		config:    cfg,
		parser:    docParser,
		chunker:   chunker,
		vectorOps: vectorOps,
	}, nil
}

// IngestFile processes a single docubilder file.
func (s *Service) IngestFile(ctx context.Context, filePath string) error {
	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	// Parse document
	doc, err := s.parser.ParseDocument(content, filePath)
	if err != nil {
		return fmt.Errorf("failed to parse document %s: %w", filePath, err)
	}

	// Chunk document
	chunks, err := s.chunker.ChunkWithHierarchy(doc)
	if err != nil {
		return fmt.Errorf("failed to chunk document %s: %w", filePath, err)
	}
	doc.Chunks = make([]models.Chunk, len(chunks))
	for i, chunk := range chunks {
		doc.Chunks[i] = *chunk
	}

	// Ingest into vector database
	err = s.vectorOps.IngestDocument(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to ingest document %s: %w", filePath, err)
	}

	return nil
}

// IngestDirectory processes all docubilder files in a directory.
func (s *Service) IngestDirectory(ctx context.Context, dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	var processed int
	var failed int

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Check if file has .md extension
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		filePath := filepath.Join(dirPath, entry.Name())
		if err := s.IngestFile(ctx, filePath); err != nil {
			// Log error to stderr instead of using fmt.Fprintf
			_, _ = fmt.Fprintf(os.Stderr, "Failed to ingest %s: %v\n", filePath, err)
			failed++
		} else {
			processed++
		}
	}

	// Log results to stdout instead of using fmt.Printf
	_, _ = fmt.Printf("Processed: %d, Failed: %d\n", processed, failed)
	return nil
}

// Search performs a semantic search.
func (s *Service) Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error) {
	return s.vectorOps.Search(ctx, query, limit, filters)
}

// QueryDebugInfo describes what was retrieved and sent to the LLM.
type QueryDebugInfo struct {
	Model   string
	Results []models.SearchResult
	Context string
	System  string
	Prompt  string
}

// Query performs a search and generates a natural language response using LLM.
func (s *Service) Query(ctx context.Context, query string, limit int) (string, error) {
	response, _, err := s.QueryDebug(ctx, query, limit)
	return response, err
}

// QueryDebug performs a search and generates a response, returning debug information
// about the retrieved chunks and constructed prompt.
func (s *Service) QueryDebug(ctx context.Context, query string, limit int) (string, *QueryDebugInfo, error) {
	// Search for relevant chunks
	results, err := s.vectorOps.Search(ctx, query, limit, nil)
	if err != nil {
		return "", nil, fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		return "No relevant information found.", nil, nil
	}

	contextItems := buildQueryContextItems(results)
	prompt, systemPrompt, err := buildQueryPrompt(query, contextItems)
	if err != nil {
		return "", nil, fmt.Errorf("prompt build failed: %w", err)
	}

	model := s.config.Ollama.GenerationModel
	llmClient := vector.NewOllamaLLMClientWithTimeout(s.config.Ollama.BaseURL, model, s.config.Ollama.Timeout)

	response, err := llmClient.Generate(ctx, prompt)
	if err != nil {
		return "", nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	return response, &QueryDebugInfo{
		Model:   model,
		Results: results,
		Context: strings.Join(contextItems, "\n\n"),
		System:  systemPrompt,
		Prompt:  prompt,
	}, nil
}

// ListDocuments returns all ingested documents.
func (s *Service) ListDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	return s.vectorOps.GetUniqueDocuments(ctx)
}

// GetDocument retrieves a specific document.
func (s *Service) GetDocument(ctx context.Context, documentID string) (*models.Document, error) {
	chunks, err := s.vectorOps.GetDocumentChunks(ctx, documentID)
	if err != nil {
		return nil, err
	}

	if len(chunks) == 0 {
		return nil, models.ErrNotFound
	}

	// Reconstruct document from chunks
	doc := &models.Document{
		ID:          documentID,
		UID:         chunks[0].UID,
		Fingerprint: chunks[0].Fingerprint,
		Title:       chunks[0].DocumentTitle,
		Chunks:      make([]models.Chunk, len(chunks)),
	}

	for i, chunk := range chunks {
		doc.Chunks[i] = *chunk
	}

	return doc, nil
}

// DeleteDocument removes a document from the system.
func (s *Service) DeleteDocument(ctx context.Context, documentID string) error {
	return s.vectorOps.DeleteDocument(ctx, documentID)
}

// GetStats returns system statistics.
func (s *Service) GetStats(ctx context.Context) (map[string]any, error) {
	count, err := s.vectorOps.GetStats()
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"total_chunks":     count,
		"embedding_model":  s.config.Ollama.EmbeddingModel,
		"generation_model": s.config.Ollama.GenerationModel,
		"collection_name":  s.config.VectorDB.CollectionName,
	}, nil
}

// ValidateConnection checks if all services are accessible.
func (s *Service) ValidateConnection(ctx context.Context) error {
	return s.vectorOps.ValidateConnection(ctx)
}

// SearchByDocument searches within a specific document.
func (s *Service) SearchByDocument(ctx context.Context, query string, documentID string, limit int) ([]models.SearchResult, error) {
	filters := map[string]string{
		"document_id": documentID,
	}
	return s.Search(ctx, query, limit, filters)
}

// GetChunk retrieves a specific chunk.
func (s *Service) GetChunk(ctx context.Context, chunkID string) (*models.Chunk, error) {
	return s.vectorOps.GetChunk(ctx, chunkID)
}

// DeleteChunk removes a specific chunk.
func (s *Service) DeleteChunk(ctx context.Context, chunkID string) error {
	return s.vectorOps.DeleteChunk(ctx, chunkID)
}

// QueryWithContext generates a response with additional context.
func (s *Service) QueryWithContext(ctx context.Context, query string, context string, limit int) (string, error) {
	// Search for relevant chunks if context is not provided
	if context == "" {
		results, err := s.vectorOps.Search(ctx, query, limit, nil)
		if err != nil {
			return "", fmt.Errorf("search failed: %w", err)
		}

		if len(results) == 0 {
			return "No relevant information found.", nil
		}

		// Build context from results
		var contextBuilder strings.Builder
		for i, result := range results {
			contextBuilder.WriteString(fmt.Sprintf("Result %d (from %s):\n%s\n\n", i+1, result.DocumentTitle, result.Content))
		}
		context = contextBuilder.String()
	}

	// Generate LLM response
	llmClient := vector.NewOllamaLLMClientWithTimeout(s.config.Ollama.BaseURL, s.config.Ollama.GenerationModel, s.config.Ollama.Timeout)
	prompt := fmt.Sprintf("Based on the following context, answer the question: %s\n\nContext:\n%s", query, context)

	response, err := llmClient.Generate(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM generation failed: %w", err)
	}

	return response, nil
}

// IngestText processes raw text content as a document.
func (s *Service) IngestText(ctx context.Context, text string, uid string, tags []string, categories []string, urls []string) error {
	// Create a document from text
	doc := models.NewDocument()
	doc.UID = uid
	doc.Tags = tags
	doc.Categories = categories
	doc.URLs = urls
	doc.Content = text
	doc.Title = "Text Document"

	// Generate fingerprint
	doc.Fingerprint = s.parser.GenerateFingerprint(text)
	// Use UID as the stable document ID; fingerprint remains content-based.
	doc.ID = doc.UID

	// Validate
	if err := doc.Validate(); err != nil {
		return fmt.Errorf("document validation failed: %w", err)
	}

	// Chunk document
	chunks, err := s.chunker.ChunkWithHierarchy(doc)
	if err != nil {
		return fmt.Errorf("failed to chunk document: %w", err)
	}
	doc.Chunks = make([]models.Chunk, len(chunks))
	for i, chunk := range chunks {
		doc.Chunks[i] = *chunk
	}

	// Ingest into vector database
	err = s.vectorOps.IngestDocument(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to ingest document: %w", err)
	}

	return nil
}

// IngestDocument processes a docubilder document from raw content.
func (s *Service) IngestDocument(ctx context.Context, content string) (*models.Document, error) {
	// Parse document
	doc, err := s.parser.ParseDocument([]byte(content), "web_upload")
	if err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}

	// Chunk document
	chunks, err := s.chunker.ChunkWithHierarchy(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to chunk document: %w", err)
	}
	doc.Chunks = make([]models.Chunk, len(chunks))
	for i, chunk := range chunks {
		doc.Chunks[i] = *chunk
	}

	// Ingest into vector database
	err = s.vectorOps.IngestDocument(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf("failed to ingest document: %w", err)
	}

	return doc, nil
}

// QueryWithLLM generates a response using the LLM with optional conversation history.
func (s *Service) QueryWithLLM(ctx context.Context, query string, model string, history []struct {
	Role    string
	Content string
}) (string, []struct {
	ID      string
	Content string
	Score   float64
}, error,
) {
	// Search for relevant chunks
	results, err := s.vectorOps.Search(ctx, query, 5, nil)
	if err != nil {
		return "", nil, fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		return "No relevant information found.", nil, nil
	}

	// Build context from results
	var contextBuilder strings.Builder
	sources := make([]struct {
		ID      string
		Content string
		Score   float64
	}, len(results))

	for i, result := range results {
		contextBuilder.WriteString(fmt.Sprintf("Result %d (from %s):\n%s\n\n", i+1, result.DocumentTitle, result.Content))
		sources[i] = struct {
			ID      string
			Content string
			Score   float64
		}{
			ID:      result.ChunkID,
			Content: result.Content,
			Score:   float64(result.Similarity),
		}
	}
	context := contextBuilder.String()

	// Build prompt with history
	var promptBuilder strings.Builder
	for _, h := range history {
		promptBuilder.WriteString(fmt.Sprintf("%s: %s\n", h.Role, h.Content))
	}
	prompt := promptBuilder.String() + fmt.Sprintf("assistant: Based on the following context, answer the question: %s\n\nContext:\n%s", query, context)

	// Generate LLM response
	llmClient := vector.NewOllamaLLMClientWithTimeout(s.config.Ollama.BaseURL, model, s.config.Ollama.Timeout)
	response, err := llmClient.Generate(ctx, prompt)
	if err != nil {
		return "", nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	return response, sources, nil
}

// CheckHealth validates service health.
func (s *Service) CheckHealth(ctx context.Context) (bool, error) {
	err := s.ValidateConnection(ctx)
	if err != nil {
		return false, err
	}
	return true, nil
}
