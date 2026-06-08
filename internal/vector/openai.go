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

// OpenAIMessage represents a single message in an OpenAI Chat Completions request.
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIChatRequest is the body posted to /v1/chat/completions.
//
// Extra fields placed in `Options` are merged into the top-level body so that
// servers can accept vendor-specific knobs (for example, `reasoning_effort`).
type OpenAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Temperature *float64        `json:"temperature,omitempty"`
	Options     map[string]any  `json:"-"`
}

// openAIChatResponse matches the standard /v1/chat/completions response shape.
type openAIChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int           `json:"index"`
		Message      OpenAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// OpenAILLMClient talks to any server that implements the OpenAI
// Chat Completions API (vLLM, llama.cpp server, LM Studio, llama-stack,
// OpenRouter, OpenAI, etc.).
type OpenAILLMClient struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

// NewOpenAILLMClient creates a client with sensible defaults.
//
// Defaults match the local Ollama setup so existing users can keep working
// without changing config: base URL http://localhost:11434 and timeout 60s.
func NewOpenAILLMClient(baseURL, model string) *OpenAILLMClient {
	return NewOpenAILLMClientWithOptions(baseURL, model, "", 60*time.Second)
}

// NewOpenAILLMClientWithOptions is the fully-configurable constructor.
// apiKey is sent as `Authorization: Bearer <key>` when non-empty.
func NewOpenAILLMClientWithOptions(baseURL, model, apiKey string, timeout time.Duration) *OpenAILLMClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "gemma:2b"
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	return &OpenAILLMClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Chat sends a list of messages to /v1/chat/completions and returns the
// assistant's reply. Options is a free-form map merged into the top-level
// request body, so callers can pass `temperature`, `top_p`, etc.
//
// Returns models.ErrGenerationFailed when the response has no choices or the
// first choice's content is empty.
func (c *OpenAILLMClient) Chat(ctx context.Context, messages []OpenAIMessage, options map[string]any) (string, error) {
	if len(messages) == 0 {
		return "", models.ErrGenerationFailed
	}
	return c.do(ctx, messages, options)
}

// ChatWithSystem is a convenience wrapper that builds a system + user pair.
func (c *OpenAILLMClient) ChatWithSystem(ctx context.Context, systemPrompt, userPrompt string, options map[string]any) (string, error) {
	if userPrompt == "" {
		return "", models.ErrGenerationFailed
	}
	messages := make([]OpenAIMessage, 0, 2)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, OpenAIMessage{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, OpenAIMessage{Role: "user", Content: userPrompt})
	return c.do(ctx, messages, options)
}

// GetModel returns the model name configured on the client.
func (c *OpenAILLMClient) GetModel() string {
	return c.model
}

// GetBaseURL returns the chat-completions base URL.
func (c *OpenAILLMClient) GetBaseURL() string {
	return c.baseURL
}

// ValidateConnection checks the server is reachable.
//
// OpenAI-compatible servers expose GET /v1/models (most) or GET /models.
// We try /v1/models first and fall back to /models for servers that
// don't version the path (e.g. llama.cpp < some version).
func (c *OpenAILLMClient) ValidateConnection(ctx context.Context) error {
	for _, path := range []string{"/v1/models", "/models"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("failed to create validation request: %w", err)
		}
		c.applyAuth(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("chat server not accessible at %s: %w", c.baseURL, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return nil
		}
		if resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("chat server returned status %d", resp.StatusCode)
		}
	}
	return fmt.Errorf("chat server at %s did not respond on /v1/models or /models", c.baseURL)
}

func (c *OpenAILLMClient) do(ctx context.Context, messages []OpenAIMessage, options map[string]any) (string, error) {
	req := OpenAIChatRequest{
		Model:    c.model,
		Messages: messages,
		Stream:   false,
		Options:  options,
	}

	// Marshal the request into a generic map so we can merge Options at the
	// top level (for keys like `temperature`, `top_p`, etc.).
	body, err := buildChatBody(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to call chat API: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chat API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed openAIChatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("chat API error: %s", parsed.Error.Message)
	}

	if len(parsed.Choices) == 0 {
		return "", models.ErrGenerationFailed
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", models.ErrGenerationFailed
	}

	return content, nil
}

func (c *OpenAILLMClient) applyAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

// buildChatBody marshals the request, then merges OpenAIChatRequest.Options
// into the top-level JSON object. Known scalar fields take precedence.
func buildChatBody(req OpenAIChatRequest) ([]byte, error) {
	if len(req.Options) == 0 {
		return json.Marshal(req)
	}

	out := make(map[string]any, len(req.Options)+4)
	maps.Copy(out, req.Options)
	out["model"] = req.Model
	out["messages"] = req.Messages
	out["stream"] = req.Stream
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	return json.Marshal(out)
}
