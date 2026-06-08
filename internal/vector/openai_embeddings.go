package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/ragabast/internal/models"
)

// OpenAIEmbeddingRequest is the body posted to /v1/embeddings.
//
// `Options` is merged into the top-level request body so callers can pass
// vendor-specific fields like `encoding_format` or `dimensions`.
type OpenAIEmbeddingRequest struct {
	Model   string         `json:"model"`
	Input   []string       `json:"input"`
	Options map[string]any `json:"-"`
}

// openAIEmbeddingResponse matches the standard /v1/embeddings response shape.
type openAIEmbeddingResponse struct {
	Object string `json:"object"`
	Model  string `json:"model"`
	Data   []struct {
		Object    string    `json:"object"`
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage *struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// OpenAIEmbeddingClient talks to any server that implements the OpenAI
// Embeddings API (Ollama 0.5+, vLLM, llama.cpp --embedding, LM Studio,
// llama-stack, OpenAI, etc.).
type OpenAIEmbeddingClient struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

// NewOpenAIEmbeddingClient creates a client with sensible defaults that
// match a local Ollama setup (http://localhost:11434, no auth).
func NewOpenAIEmbeddingClient(baseURL, model string) *OpenAIEmbeddingClient {
	return NewOpenAIEmbeddingClientWithOptions(baseURL, model, "", 30*time.Second)
}

// NewOpenAIEmbeddingClientWithOptions is the fully-configurable constructor.
// apiKey is sent as `Authorization: Bearer <key>` when non-empty.
func NewOpenAIEmbeddingClientWithOptions(baseURL, model, apiKey string, timeout time.Duration) *OpenAIEmbeddingClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-ai/nomic-embed-text-v1.5"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &OpenAIEmbeddingClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// GenerateEmbedding generates an embedding for the given text.
func (c *OpenAIEmbeddingClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, models.ErrEmbeddingFailed
	}
	resp, err := c.embed(ctx, []string{text}, nil)
	if err != nil {
		return nil, err
	}
	if len(resp) == 0 || len(resp[0]) == 0 {
		return nil, models.ErrEmbeddingFailed
	}
	return resp[0], nil
}

// GenerateEmbeddingsBatch generates embeddings for multiple texts in batch.
//
// Servers like vLLM and OpenAI accept an array `input`; we send one batched
// request per call. Some servers (older Ollama) only accept a single string;
// this client always sends a 1-element array, which all conformant servers
// accept.
func (c *OpenAIEmbeddingClient) GenerateEmbeddingsBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, models.ErrEmbeddingFailed
	}
	return c.embed(ctx, texts, nil)
}

// GetModel returns the model name configured on the client.
func (c *OpenAIEmbeddingClient) GetModel() string {
	return c.model
}

// GetBaseURL returns the embeddings base URL.
func (c *OpenAIEmbeddingClient) GetBaseURL() string {
	return c.baseURL
}

// ValidateConnection checks the server is reachable.
//
// Tries /v1/models first, then /models for servers that don't version
// the path. Returns nil on any 2xx response.
func (c *OpenAIEmbeddingClient) ValidateConnection(ctx context.Context) error {
	for _, path := range []string{"/v1/models", "/models"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("failed to create validation request: %w", err)
		}
		c.applyAuth(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("embeddings server not accessible at %s: %w", c.baseURL, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return nil
		}
		if resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("embeddings server returned status %d", resp.StatusCode)
		}
	}
	return fmt.Errorf("embeddings server at %s did not respond on /v1/models or /models", c.baseURL)
}

// GenerateChunkEmbeddings generates embeddings for a batch of chunks.
func (c *OpenAIEmbeddingClient) GenerateChunkEmbeddings(ctx context.Context, chunks []*models.Chunk) ([][]float32, error) {
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
func (c *OpenAIEmbeddingClient) GenerateChunkEmbedding(ctx context.Context, chunk *models.Chunk) ([]float32, error) {
	if chunk == nil {
		return nil, models.ErrEmbeddingFailed
	}

	return c.GenerateEmbedding(ctx, chunk.GetFullPath())
}

func (c *OpenAIEmbeddingClient) embed(ctx context.Context, texts []string, options map[string]any) ([][]float32, error) {
	req := OpenAIEmbeddingRequest{
		Model:   c.model,
		Input:   texts,
		Options: options,
	}

	body, err := buildEmbeddingBody(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/embeddings", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to call embeddings API: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed openAIEmbeddingResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("embeddings API error: %s", parsed.Error.Message)
	}

	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings API returned %d vectors for %d inputs", len(parsed.Data), len(texts))
	}

	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("embeddings API returned empty vector for input %d: %w", i, models.ErrEmbeddingFailed)
		}
		out[i] = d.Embedding
	}
	return out, nil
}

func (c *OpenAIEmbeddingClient) applyAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// buildEmbeddingBody marshals the request, merging OpenAIEmbeddingRequest.Options
// into the top-level JSON object. Model and Input take precedence.
func buildEmbeddingBody(req OpenAIEmbeddingRequest) ([]byte, error) {
	if len(req.Options) == 0 {
		return json.Marshal(req)
	}

	out := make(map[string]any, len(req.Options)+2)
	maps.Copy(out, req.Options)
	out["model"] = req.Model
	out["input"] = req.Input
	return json.Marshal(out)
}
