package service

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
)

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

// Search performs a semantic search.
func (s *Service) Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error) {
	return s.vectorOps.Search(ctx, query, limit, filters)
}

// SearchByDocument searches within a specific document.
func (s *Service) SearchByDocument(ctx context.Context, query string, documentID string, limit int) ([]models.SearchResult, error) {
	return s.Search(ctx, query, limit, map[string]string{"document_id": documentID})
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
	results, err := s.vectorOps.Search(ctx, query, limit, nil)
	if err != nil {
		return "", nil, fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		return "No relevant information found.", nil, nil
	}

	model := s.config.Ollama.ChatModel

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

	response, err := s.llmClient.Chat(ctx, clientMessages, llmOptions)
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
