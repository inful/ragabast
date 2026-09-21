package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"sync"
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
	baseURL           string
	model             string
	apiKey            string
	httpClient        *http.Client
	expectedDimension int    // 0 = unset; when >0, request body includes `dimensions: N` and the response is length-checked
	concurrency       int    // 1 = sequential; >1 = parallel sub-batches per GenerateChunkEmbeddings
	docPrompt         string // prepended to chunk text on the doc side; empty = no prepend
	queryPrompt       string // prepended to user query on the query side; empty = no prepend

	dimWarnOnce sync.Once
}

// NewOpenAIEmbeddingClientWithOptions is the fully-configurable constructor.
// apiKey is sent as `Authorization: Bearer <key>` when non-empty.
// Empty baseURL defaults to http://localhost:11434; empty model
// defaults to nomic-ai/nomic-embed-text-v1.5; a non-positive timeout
// defaults to 30s.
//
// dimensions controls the Matryoshka path: when >0, every request
// body carries `dimensions: <N>` so a Matryoshka-capable server
// (jina v5, OpenAI text-embedding-3-*) truncates the returned
// vector to N. The client also checks the actual response length
// and logs a one-shot DIMENSION MISMATCH warning if the server
// ignored the request.
//
// concurrency controls the parallel-worker pool used by
// GenerateChunkEmbeddings: chunks are split into
// min(concurrency, len(chunks)) sub-batches and processed
// concurrently. concurrency <= 1 disables the pool (the call
// runs sequentially, one sub-batch at a time). The default
// for the pool is 4 — a reasonable match for Ollama's
// OLLAMA_NUM_PARALLEL default.
//
// Operators match this to the embedding server's own
// parallelism headroom. Setting it higher than the server
// can handle trades CPU on the ragabast side for queue depth
// on the server side; setting it lower leaves server capacity
// unused.
func NewOpenAIEmbeddingClientWithOptions(baseURL, model, apiKey string, timeout time.Duration, dimensions int, concurrency int, docPrompt string, queryPrompt string) *OpenAIEmbeddingClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-ai/nomic-embed-text-v1.5"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if concurrency < 1 {
		concurrency = 1
	}

	return &OpenAIEmbeddingClient{
		baseURL:           normalizeOpenAIBaseURL(baseURL),
		model:             model,
		apiKey:            apiKey,
		expectedDimension: dimensions,
		concurrency:       concurrency,
		docPrompt:         docPrompt,
		queryPrompt:       queryPrompt,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// checkDimension fires a one-shot DIMENSION MISMATCH warning
// when expectedDimension is set and the actual response length
// differs. Safe to call on every embedding response; the
// sync.Once guarantees the warning fires at most once per
// client instance.
func (c *OpenAIEmbeddingClient) checkDimension(actual int) {
	if c.expectedDimension <= 0 || actual == c.expectedDimension {
		return
	}
	c.dimWarnOnce.Do(func() {
		log.Printf("vector: DIMENSION MISMATCH: configured EmbeddingDimensions=%d but embeddings server returned vectors of length %d. The server may be ignoring the dimensions request, or the model does not support truncation to that size. Search results will be incorrect until this is fixed.", c.expectedDimension, actual)
	})
}

// GenerateEmbedding generates an embedding for the given
// text on the doc side: the configured docPrompt (if any)
// is prepended before POSTing. Use EmbedQuery for user
// queries — sending a query through this method would
// prepend the doc prompt and the model would embed it as
// if it were an indexed document, measurably worse
// retrieval quality with embedding-gemma.
func (c *OpenAIEmbeddingClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, models.ErrEmbeddingFailed
	}
	resp, err := c.embed(ctx, []string{c.applyDocPrompt(text)}, nil)
	if err != nil {
		return nil, err
	}
	if len(resp) == 0 || len(resp[0]) == 0 {
		return nil, models.ErrEmbeddingFailed
	}
	return resp[0], nil
}

// EmbedQuery generates an embedding for a user query.
// The configured queryPrompt (if any) is prepended before
// POSTing. This is the embedding-gemma optimization —
// doc-side and query-side prompts differ, and using the
// wrong one silently degrades retrieval quality.
func (c *OpenAIEmbeddingClient) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	if query == "" {
		return nil, models.ErrEmbeddingFailed
	}
	resp, err := c.embed(ctx, []string{c.applyQueryPrompt(query)}, nil)
	if err != nil {
		return nil, err
	}
	if len(resp) == 0 || len(resp[0]) == 0 {
		return nil, models.ErrEmbeddingFailed
	}
	return resp[0], nil
}

// applyDocPrompt returns text with the doc prompt prepended.
// Empty prompt = identity (no prepend, no allocation
// thrash on the hot path).
func (c *OpenAIEmbeddingClient) applyDocPrompt(text string) string {
	if c.docPrompt == "" {
		return text
	}
	return c.docPrompt + text
}

// applyQueryPrompt mirrors applyDocPrompt for queries.
func (c *OpenAIEmbeddingClient) applyQueryPrompt(query string) string {
	if c.queryPrompt == "" {
		return query
	}
	return c.queryPrompt + query
}

// generateEmbeddingsBatch generates embeddings for multiple texts
// in batch. Servers like vLLM and OpenAI accept an array `input`; we
// send one batched request per call. Some servers (older Ollama)
// only accept a single string; this client always sends a 1-element
// array, which all conformant servers accept.
//
// Package-private; callers outside the embedding pipeline should use
// GenerateEmbedding (single) or GenerateChunkEmbeddings (chunk-shaped).
func (c *OpenAIEmbeddingClient) generateEmbeddingsBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, models.ErrEmbeddingFailed
	}

	// Concurrency=1 (or empty config) preserves the historical
	// single-request behavior. Useful on small ingests and on
	// servers without a parallel pipeline.
	concurrency := max(c.concurrency, 1)
	if concurrency == 1 || len(texts) == 1 {
		return c.embed(ctx, texts, nil)
	}

	// Split into at-most-`concurrency` sub-batches of roughly
	// equal size. Each sub-batch is processed by one HTTP
	// request via the existing embed(); the worker pool
	// stitches the results back in input order.
	subBatches := splitTexts(texts, concurrency)

	type result struct {
		start int
		vecs  [][]float32
		err   error
	}
	results := make([]result, len(subBatches))
	var wg sync.WaitGroup

	// errOnce collapses the first error from any worker into
	// a single return value. We don't cancel sibling workers
	// when one fails because the underlying HTTP client has
	// its own timeout; just abandon their results.
	var errOnce sync.Once
	var firstErr error

	for i, sb := range subBatches {
		start := sumLengthsUpTo(subBatches, i)
		wg.Add(1)
		go func(idx int, batch []string, sbStart int) {
			defer wg.Done()
			vecs, err := c.embed(ctx, batch, nil)
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			results[idx] = result{start: sbStart, vecs: vecs}
		}(i, sb, start)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	// Stitch per-sub-batch results back into input order. Each
	// result.start is the index in the original `texts` slice
	// where that sub-batch begins.
	out := make([][]float32, len(texts))
	for _, r := range results {
		if r.err != nil || r.vecs == nil {
			continue
		}
		copy(out[r.start:], r.vecs)
	}
	return out, nil
}

// splitTexts divides texts into at most n sub-batches of roughly
// equal size. The result length is min(n, len(texts)). The last
// sub-batch is the short one when len(texts) doesn't divide
// evenly; the worker pool handles unequal splits fine.
func splitTexts(texts []string, n int) [][]string {
	if n < 1 {
		n = 1
	}
	if n > len(texts) {
		n = len(texts)
	}
	out := make([][]string, n)
	base := len(texts) / n
	extra := len(texts) % n
	idx := 0
	for i := range n {
		size := base
		if i < extra {
			size++
		}
		out[i] = texts[idx : idx+size]
		idx += size
	}
	return out
}

// sumLengthsUpTo returns the total length of subBatches[0..i].
// Used to compute the input-slice offset each worker should
// write into when stitching results back together.
func sumLengthsUpTo(subBatches [][]string, i int) int {
	n := 0
	for j := range i {
		n += len(subBatches[j])
	}
	return n
}

// modelName returns the model name configured on the client.
// Package-private; used only by tests and a few internal
// helpers. Use field access directly where possible.
func (c *OpenAIEmbeddingClient) modelName() string {
	return c.model
}

// baseURLString returns the embeddings base URL as a string.
// Package-private; used only by tests. Use c.baseURL directly
// where possible.
func (c *OpenAIEmbeddingClient) baseURLString() string {
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
// The configured docPrompt (if any) is prepended to every
// chunk text before the POST. Empty prompt = identity (the
// historical raw-text behavior).
func (c *OpenAIEmbeddingClient) GenerateChunkEmbeddings(ctx context.Context, chunks []*models.Chunk) ([][]float32, error) {
	if len(chunks) == 0 {
		return nil, models.ErrEmbeddingFailed
	}

	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = c.applyDocPrompt(chunk.GetFullPath())
	}

	return c.generateEmbeddingsBatch(ctx, texts)
}

// GenerateChunkEmbedding generates an embedding for a single chunk.
func (c *OpenAIEmbeddingClient) GenerateChunkEmbedding(ctx context.Context, chunk *models.Chunk) ([]float32, error) {
	if chunk == nil {
		return nil, models.ErrEmbeddingFailed
	}

	return c.GenerateEmbedding(ctx, chunk.GetFullPath())
}

// // — kept in the signature for callers that may want to
// // inject per-request fields in the future.
//
//nolint:unparam // options is part of the function's documented contract
func (c *OpenAIEmbeddingClient) embed(ctx context.Context, texts []string, options map[string]any) ([][]float32, error) {
	// Inject `dimensions: N` when Matryoshka truncation is requested.
	// The Options map is merged into the top-level JSON body, so this
	// sits next to model and input.
	if c.expectedDimension > 0 {
		if options == nil {
			options = map[string]any{}
		}
		options["dimensions"] = c.expectedDimension
	}

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

	// One-shot dimension-mismatch check: only the first vector's
	// length matters, since the server returns the same dim for
	// every input in a batch.
	if len(out) > 0 {
		c.checkDimension(len(out[0]))
	}

	return out, nil
}

func (c *OpenAIEmbeddingClient) applyAuth(req *http.Request) {
	applyAuth(req, c.apiKey)
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
