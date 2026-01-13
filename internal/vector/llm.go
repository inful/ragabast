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

// OllamaGenerateRequest represents the request to generate text via Ollama.
type OllamaGenerateRequest struct {
	Model    string         `json:"model"`
	Prompt   string         `json:"prompt"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
	System   string         `json:"system,omitempty"`
	Template string         `json:"template,omitempty"`
}

// OllamaGenerateResponse represents the response from Ollama generate API.
type OllamaGenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// OllamaLLMClient handles text generation via Ollama.
type OllamaLLMClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewOllamaLLMClient creates a new Ollama LLM client.
func NewOllamaLLMClient(baseURL, model string) *OllamaLLMClient {
	return NewOllamaLLMClientWithTimeout(baseURL, model, 60*time.Second)
}

// NewOllamaLLMClientWithTimeout creates a new Ollama LLM client with a custom timeout.
func NewOllamaLLMClientWithTimeout(baseURL, model string, timeout time.Duration) *OllamaLLMClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "gemma:2b"
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	return &OllamaLLMClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Generate generates text based on the given prompt.
func (c *OllamaLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", models.ErrGenerationFailed
	}

	request := OllamaGenerateRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Ollama API: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Ollama API returned status %d: %s", resp.StatusCode, string(body))
	}

	var generateResp OllamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&generateResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if generateResp.Response == "" {
		return "", models.ErrGenerationFailed
	}

	return generateResp.Response, nil
}

// GenerateWithContext generates text with additional context.
func (c *OllamaLLMClient) GenerateWithContext(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if userPrompt == "" {
		return "", models.ErrGenerationFailed
	}

	request := OllamaGenerateRequest{
		Model:  c.model,
		Prompt: userPrompt,
		Stream: false,
		System: systemPrompt,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Ollama API: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama API returned status %d: %s", resp.StatusCode, string(body))
	}

	var generateResp OllamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&generateResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if generateResp.Response == "" {
		return "", models.ErrGenerationFailed
	}

	return generateResp.Response, nil
}

// GetModel returns the model being used.
func (c *OllamaLLMClient) GetModel() string {
	return c.model
}

// GetBaseURL returns the base URL of the Ollama service.
func (c *OllamaLLMClient) GetBaseURL() string {
	return c.baseURL
}

// ValidateConnection checks if the Ollama service is accessible.
func (c *OllamaLLMClient) ValidateConnection(ctx context.Context) error {
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
