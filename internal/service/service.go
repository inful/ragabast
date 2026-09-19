package service

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
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
	parser    *parser.DocbuilderParser
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
	embeddings := vector.NewOpenAIEmbeddingClientWithOptions(
		cfg.Ollama.BaseURL,
		cfg.Ollama.EmbeddingModel,
		cfg.Ollama.EffectiveEmbeddingAPIKey(),
		cfg.Ollama.Timeout,
	)

	// Initialize vector operations
	vectorOps := vector.NewVectorOperations(db, embeddings)

	// Initialize parser and chunker
	docParser := parser.NewDocbuilderParser()
	chunker := chunker.NewChunker(cfg.Processing.MaxChunkSize, cfg.Processing.MinChunkSize, cfg.Processing.ChunkOverlap)

	return &Service{
		config:    cfg,
		parser:    docParser,
		chunker:   chunker,
		vectorOps: vectorOps,
	}, nil
}

// IngestFile processes a single docbuilder file.
func (s *Service) IngestFile(ctx context.Context, filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	doc, err := s.parser.ParseDocument(content, filePath)
	if err != nil {
		return fmt.Errorf("failed to parse document %s: %w", filePath, err)
	}

	if err := chunkAndIngest(ctx, s.chunker, s.vectorOps, doc); err != nil {
		return fmt.Errorf("ingest %s: %w", filePath, err)
	}
	return nil
}

// IngestDirectory processes all docbuilder files in a directory.
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

// LLMOptions controls generation parameters.
type LLMOptions struct {
	Temperature *float64
	History     []ChatMessage
}

// Query performs a search and generates a natural language response using LLM.
func (s *Service) Query(ctx context.Context, query string, limit int) (string, error) {
	response, _, err := s.QueryDebugWithOptions(ctx, query, limit, LLMOptions{})
	return response, err
}

// QueryDebug performs a search and generates a response, returning debug information
// about the retrieved chunks and constructed prompt.
func (s *Service) QueryDebug(ctx context.Context, query string, limit int) (string, *QueryDebugInfo, error) {
	return s.QueryDebugWithOptions(ctx, query, limit, LLMOptions{})
}

// QueryDebugWithOptions is like QueryDebug but allows controlling LLM generation options.
func (s *Service) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts LLMOptions) (string, *QueryDebugInfo, error) {
	// Search for relevant chunks
	results, err := s.vectorOps.Search(ctx, query, limit, nil)
	if err != nil {
		return "", nil, fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		return "No relevant information found.", nil, nil
	}

	model := s.config.Ollama.ChatModel
	llmClient := vector.NewOpenAILLMClientWithOptions(
		s.config.Ollama.ChatBaseURL,
		model,
		s.config.Ollama.EffectiveChatAPIKey(),
		s.config.Ollama.Timeout,
	)

	llmOptions := map[string]any{}
	maps.Copy(llmOptions, s.config.Ollama.Options)
	if opts.Temperature != nil {
		llmOptions["temperature"] = *opts.Temperature
	} else if s.config.Ollama.Temperature != nil {
		llmOptions["temperature"] = *s.config.Ollama.Temperature
	}

	contextItems := buildQueryContextItems(ctx, results, vectorChunkFetcher{s: s})
	messages, err := buildQueryMessages(query, contextItems, opts.History)
	if err != nil {
		return "", nil, fmt.Errorf("prompt build failed: %w", err)
	}

	// Convert to the vector-package message type for the client.
	clientMessages := make([]vector.OpenAIMessage, len(messages))
	for i, m := range messages {
		clientMessages[i] = vector.OpenAIMessage{Role: m.Role, Content: m.Content}
	}

	response, err := llmClient.Chat(ctx, clientMessages, llmOptions)
	if err != nil {
		return "", nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	response = appendLinksSection(response, extractURLs(results))

	return response, &QueryDebugInfo{
		Model:   model,
		Results: results,
		Context: strings.Join(contextItems, "\n\n"),
		System:  messages[0].Content,
		Prompt:  messages[1].Content,
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
		"total_chunks":    count,
		"embedding_model": s.config.Ollama.EmbeddingModel,
		"chat_model":      s.config.Ollama.ChatModel,
		"collection_name": s.config.VectorDB.CollectionName,
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

// IngestText processes raw text content as a document.
func (s *Service) IngestText(ctx context.Context, text string, uid string, tags []string, categories []string, urls []string) error {
	doc := models.NewDocument()
	doc.UID = uid
	doc.Tags = tags
	doc.Categories = categories
	doc.URLs = urls
	doc.Content = text
	doc.Title = "Text Document"
	doc.Fingerprint = s.parser.GenerateFingerprint(text)
	doc.ID = doc.UID

	if err := doc.Validate(); err != nil {
		return fmt.Errorf("document validation failed: %w", err)
	}

	return chunkAndIngest(ctx, s.chunker, s.vectorOps, doc)
}

// IngestDocument processes a docbuilder document from raw content.
func (s *Service) IngestDocument(ctx context.Context, content string) (*models.Document, error) {
	doc, err := s.parser.ParseDocument([]byte(content), "web_upload")
	if err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}

	if err := chunkAndIngest(ctx, s.chunker, s.vectorOps, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// GetNormalizedTags retrieves all unique tags from the vector database, normalized to lowercase.
func (s *Service) GetNormalizedTags(ctx context.Context) ([]string, error) {
	docs, err := s.vectorOps.GetUniqueDocuments(ctx)
	if err != nil {
		return nil, err
	}

	tagSet := make(map[string]bool)
	for _, doc := range docs {
		for _, tag := range doc.Tags {
			normalized := strings.ToLower(strings.TrimSpace(tag))
			if normalized != "" {
				tagSet[normalized] = true
			}
		}
	}

	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}

	// Sort for consistent ordering
	return sortStrings(tags), nil
}

// GetNormalizedCategories retrieves all unique categories from the vector database.
// Categories are kept as-is (preserving capitalization) but trimmed of whitespace.
func (s *Service) GetNormalizedCategories(ctx context.Context) ([]string, error) {
	docs, err := s.vectorOps.GetUniqueDocuments(ctx)
	if err != nil {
		return nil, err
	}

	categorySet := make(map[string]bool)
	for _, doc := range docs {
		for _, category := range doc.Categories {
			normalized := strings.TrimSpace(category)
			if normalized != "" {
				categorySet[normalized] = true
			}
		}
	}

	categories := make([]string, 0, len(categorySet))
	for category := range categorySet {
		categories = append(categories, category)
	}

	// Sort for consistent ordering
	return sortStrings(categories), nil
}

// GetTagsAndCategories retrieves both normalized tags and categories.
func (s *Service) GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error) {
	tags, err = s.GetNormalizedTags(ctx)
	if err != nil {
		return nil, nil, err
	}

	categories, err = s.GetNormalizedCategories(ctx)
	if err != nil {
		return nil, nil, err
	}

	return tags, categories, nil
}

// CheckHealth validates service health.
func (s *Service) CheckHealth(ctx context.Context) (bool, error) {
	err := s.ValidateConnection(ctx)
	if err != nil {
		return false, err
	}
	return true, nil
}

// sortStrings is a helper to sort string slices in place and return them.
func sortStrings(strs []string) []string {
	sort.Strings(strs)
	return strs
}

// vectorChunkFetcher adapts the prompt layer's ChunkFetcher interface
// to the Service's own GetChunk method, so the prompt builder can
// pull parent context without depending on the vector package.
type vectorChunkFetcher struct {
	s *Service
}

func (v vectorChunkFetcher) FetchChunk(ctx context.Context, id string) (*models.Chunk, bool, error) {
	chunk, err := v.s.GetChunk(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if chunk == nil {
		return nil, false, nil
	}
	return chunk, true, nil
}
