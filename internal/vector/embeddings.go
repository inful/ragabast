package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ragabast/internal/models"
)

// OllamaEmbeddingRequest represents the request to generate embeddings via Ollama.
type OllamaEmbeddingRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options,omitempty"`
}

// OllamaEmbeddingResponse represents the response from Ollama embedding API.
type OllamaEmbeddingResponse struct {
	Embedding []float32 `json:"embedding"`
}

// OllamaEmbeddingClient handles embedding generation via Ollama.
type OllamaEmbeddingClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewOllamaEmbeddingClient creates a new Ollama embedding client.
func NewOllamaEmbeddingClient(baseURL, model string) *OllamaEmbeddingClient {
	return NewOllamaEmbeddingClientWithTimeout(baseURL, model, 30*time.Second)
}

// NewOllamaEmbeddingClientWithTimeout creates a new Ollama embedding client with a custom timeout.
func NewOllamaEmbeddingClientWithTimeout(baseURL, model string, timeout time.Duration) *OllamaEmbeddingClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-ai/nomic-embed-text-v1.5"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &OllamaEmbeddingClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// GenerateEmbedding generates an embedding for the given text.
func (c *OllamaEmbeddingClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, models.ErrEmbeddingFailed
	}

	request := OllamaEmbeddingRequest{
		Model:  c.model,
		Prompt: text,
		Stream: false,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call Ollama API: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Ollama API returned status %d: %s", resp.StatusCode, string(body))
	}

	var embeddingResp OllamaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embeddingResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(embeddingResp.Embedding) == 0 {
		return nil, models.ErrEmbeddingFailed
	}

	return embeddingResp.Embedding, nil
}

// GenerateEmbeddingsBatch generates embeddings for multiple texts in batch.
func (c *OllamaEmbeddingClient) GenerateEmbeddingsBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, models.ErrEmbeddingFailed
	}

	embeddings := make([][][]float32, len(texts))

	// Process each text individually (Ollama doesn't support batch embeddings in one call)
	for i, text := range texts {
		embedding, err := c.GenerateEmbedding(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("failed to generate embedding for text %d: %w", i, err)
		}
		embeddings[i] = [][]float32{embedding}
	}

	// Flatten the result
	result := make([][]float32, len(texts))
	for i, emb := range embeddings {
		result[i] = emb[0]
	}

	return result, nil
}

// GetModel returns the model being used.
func (c *OllamaEmbeddingClient) GetModel() string {
	return c.model
}

// GetBaseURL returns the base URL of the Ollama service.
func (c *OllamaEmbeddingClient) GetBaseURL() string {
	return c.baseURL
}

// ValidateConnection checks if the Ollama service is accessible.
func (c *OllamaEmbeddingClient) ValidateConnection(ctx context.Context) error {
	// Try to get model list endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("failed to create validation request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama service not accessible at %s: %w", c.baseURL, err)
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama service returned status %d", resp.StatusCode)
	}

	return nil
}

// GenerateChunkEmbeddings generates embeddings for a batch of chunks.
func (c *OllamaEmbeddingClient) GenerateChunkEmbeddings(ctx context.Context, chunks []*models.Chunk) ([][]float32, error) {
	if len(chunks) == 0 {
		return nil, models.ErrEmbeddingFailed
	}

	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		// Use the full hierarchical path for better context
		texts[i] = chunk.GetFullPath()
	}

	return c.GenerateEmbeddingsBatch(ctx, texts)
}

// GenerateChunkEmbedding generates an embedding for a single chunk.
func (c *OllamaEmbeddingClient) GenerateChunkEmbedding(ctx context.Context, chunk *models.Chunk) ([]float32, error) {
	if chunk == nil {
		return nil, models.ErrEmbeddingFailed
	}

	return c.GenerateEmbedding(ctx, chunk.GetFullPath())
}
