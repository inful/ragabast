package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ragabast/internal/models"
)

// EmbeddingGemmaRequest is the body posted to /v2/embed.
//
// The EmbeddingGemma wire shape is intentionally minimal:
// a single `texts` array, no `model`, no `dimensions`, no
// `encoding_format`. The server picks the model itself and
// returns vectors at the dimension it was started with.
// Operators who need Matryoshka truncation have to
// configure it server-side; ragabast doesn't pass it
// through.
type EmbeddingGemmaRequest struct {
	Texts []string `json:"texts"`
}

// embeddingGemmaResponse tries to be flexible about the
// exact response shape, since the /v2/embed endpoint isn't
// a formal standard. The shape probe in decodeEmbeddingGemma
// accepts any of the three common forms:
//
//  1. {"embeddings": [[...], [...]]}        — parallel-of-texts
//  2. {"results":     [{"embedding": [...]}]}
//  3. {"data":        [{"embedding": [...]}]} — OpenAI-style
//
// We don't pre-decode into a struct: probing in raw
// map[string]any lets us inspect the body when none of the
// expected fields are present and include the body in the
// error message so the operator can pin down the actual
// shape their server uses.
type embeddingGemmaResponse map[string]any

// EmbeddingGemmaClient talks to an EmbeddingGemma /v2/embed
// server. The wire shape is:
//
//	POST <baseURL>/v2/embed
//	Content-Type: application/json
//	Body:         {"texts": ["...", "..."]}
//
// The server picks the model — the client doesn't send a
// `model` field. Likewise there's no Authorization header;
// the /v2/embed endpoint is unauthenticated by convention.
//
// Two design choices to be aware of:
//
//   - Response shape: this client probes for `embeddings`
//     first, then `results`, then `data`. If none match, the
//     error wraps the raw response body so the operator can
//     see what came back and either patch this client or
//     their proxy.
//   - Doc / query prompts: the configured docPrompt /
//     queryPrompt are prepended to each text before the
//     POST. The server is responsible for interpreting them
//     — typically the recommended embedding-gemma prompts
//     `title: none | text:` and `task: search result |
//     query:`.
type EmbeddingGemmaClient struct {
	baseURL           string
	httpClient        *http.Client
	expectedDimension int    // 0 = unset; when >0, response vectors are length-checked and a one-shot mismatch is logged
	concurrency       int    // 1 = sequential; >1 = parallel sub-batches per GenerateChunkEmbeddings
	docPrompt         string // prepended to chunk text on the doc side; empty = no prepend
	queryPrompt       string // prepended to user query on the query side; empty = no prepend

	dimWarnOnce sync.Once
}

// NewEmbeddingGemmaClientWithOptions is the fully-configurable
// constructor. baseURL is the server root; the client appends
// `/v2/embed` itself. Empty baseURL defaults to
// http://localhost:11434 (same default as the OpenAI client);
// a non-positive timeout defaults to 30s; concurrency < 1
// defaults to 1.
//
// dimensions controls the dimension-mismatch check: when
// >0, the client verifies each response vector has that
// length and logs a one-shot DIMENSION MISMATCH warning if
// not. The EmbeddingGemma wire shape doesn't carry a
// `dimensions` field — operators have to match the server's
// configured dimension via vectordb.embedding_dimension.
func NewEmbeddingGemmaClientWithOptions(baseURL string, timeout time.Duration, dimensions int, concurrency int, docPrompt string, queryPrompt string) *EmbeddingGemmaClient {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if concurrency < 1 {
		concurrency = 1
	}

	return &EmbeddingGemmaClient{
		baseURL:           normalizeOpenAIBaseURL(baseURL),
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
// client instance. Mirrors OpenAIEmbeddingClient.checkDimension
// so operators see the same warning text regardless of which
// provider they're using.
func (c *EmbeddingGemmaClient) checkDimension(actual int) {
	if c.expectedDimension <= 0 || actual == c.expectedDimension {
		return
	}
	c.dimWarnOnce.Do(func() {
		log.Printf("vector: DIMENSION MISMATCH: configured EmbeddingDimensions=%d but the embeddings server returned vectors of length %d. "+
			"The server may be ignoring the dimensions request, or the model does not support truncation to that size. "+
			"Search results will be incorrect until this is fixed.", c.expectedDimension, actual)
	})
}

// GenerateEmbedding generates an embedding for the given
// text on the doc side: the configured docPrompt (if any)
// is prepended before POSTing. Use EmbedQuery for user
// queries — sending a query through this method would
// prepend the doc prompt and the model would embed it as
// if it were an indexed document, measurably worse
// retrieval quality with embedding-gemma.
func (c *EmbeddingGemmaClient) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, models.ErrEmbeddingFailed
	}
	resp, err := c.embed(ctx, []string{c.applyDocPrompt(text)})
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
// POSTing. Mirrors OpenAIEmbeddingClient.EmbedQuery so the
// embedding-gemma doc/query split works the same way across
// providers.
func (c *EmbeddingGemmaClient) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	if query == "" {
		return nil, models.ErrEmbeddingFailed
	}
	resp, err := c.embed(ctx, []string{c.applyQueryPrompt(query)})
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
func (c *EmbeddingGemmaClient) applyDocPrompt(text string) string {
	if c.docPrompt == "" {
		return text
	}
	return c.docPrompt + text
}

// applyQueryPrompt mirrors applyDocPrompt for queries.
func (c *EmbeddingGemmaClient) applyQueryPrompt(query string) string {
	if c.queryPrompt == "" {
		return query
	}
	return c.queryPrompt + query
}

// GenerateChunkEmbeddings generates embeddings for a batch
// of chunks. The configured docPrompt (if any) is prepended
// to every chunk text before the POST. Empty prompt =
// identity (raw text). When concurrency > 1, the input is
// split into min(concurrency, len(chunks)) sub-batches and
// processed in parallel.
func (c *EmbeddingGemmaClient) GenerateChunkEmbeddings(ctx context.Context, chunks []*models.Chunk) ([][]float32, error) {
	if len(chunks) == 0 {
		return nil, models.ErrEmbeddingFailed
	}

	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = c.applyDocPrompt(chunk.GetFullPath())
	}

	return c.generateEmbeddingsBatch(ctx, texts)
}

// GenerateChunkEmbedding generates an embedding for a
// single chunk.
func (c *EmbeddingGemmaClient) GenerateChunkEmbedding(ctx context.Context, chunk *models.Chunk) ([]float32, error) {
	if chunk == nil {
		return nil, models.ErrEmbeddingFailed
	}

	return c.GenerateEmbedding(ctx, chunk.GetFullPath())
}

// generateEmbeddingsBatch generates embeddings for multiple
// texts in batch. The /v2/embed endpoint accepts a `texts`
// array, so we send one batched request per call. When
// concurrency > 1 the input is split across min(concurrency,
// len(texts)) sub-batches and processed concurrently.
// Package-private; callers outside the embedding pipeline
// should use GenerateEmbedding (single) or
// GenerateChunkEmbeddings (chunk-shaped).
func (c *EmbeddingGemmaClient) generateEmbeddingsBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, models.ErrEmbeddingFailed
	}

	// Concurrency=1 (or empty config) preserves the
	// historical single-request behavior. Useful on small
	// ingests and on servers without a parallel pipeline.
	concurrency := max(c.concurrency, 1)
	if concurrency == 1 || len(texts) == 1 {
		return c.embed(ctx, texts)
	}

	// Split into at-most-`concurrency` sub-batches of
	// roughly equal size. Each sub-batch is processed by
	// one HTTP request via the existing embed(); the
	// worker pool stitches the results back in input order.
	subBatches := splitTexts(texts, concurrency)

	type result struct {
		start int
		vecs  [][]float32
		err   error
	}
	results := make([]result, len(subBatches))
	var wg sync.WaitGroup

	// errOnce collapses the first error from any worker
	// into a single return value. We don't cancel sibling
	// workers when one fails because the underlying HTTP
	// client has its own timeout; just abandon their
	// results.
	var errOnce sync.Once
	var firstErr error

	for i, sb := range subBatches {
		start := sumLengthsUpTo(subBatches, i)
		wg.Add(1)
		go func(idx int, batch []string, sbStart int) {
			defer wg.Done()
			vecs, err := c.embed(ctx, batch)
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

	// Stitch per-sub-batch results back into input order.
	// Each result.start is the index in the original
	// `texts` slice where that sub-batch begins.
	out := make([][]float32, len(texts))
	for _, r := range results {
		if r.err != nil || r.vecs == nil {
			continue
		}
		copy(out[r.start:], r.vecs)
	}
	return out, nil
}

// ValidateConnection sends a probe embedding to verify the
// server is reachable. We POST a tiny "texts" array to
// /v2/embed and check for a 2xx status — same shape as a
// real call, which means it also catches auth-required
// servers that the OpenAI client's `/v1/models` probe
// would miss.
func (c *EmbeddingGemmaClient) ValidateConnection(ctx context.Context) error {
	body, err := json.Marshal(EmbeddingGemmaRequest{Texts: []string{"ping"}})
	if err != nil {
		return fmt.Errorf("failed to marshal probe body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/embed", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create validation request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("embeddings server not accessible at %s: %w", c.baseURL, err)
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		// Read up to 4 KiB of the body so the operator
		// can see what went wrong without paging through
		// a multi-megabyte error page.
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("embeddings server returned status %d: %s", resp.StatusCode, string(preview))
	}
	return nil
}

// baseURLString returns the embeddings base URL as a string.
// Package-private; used only by tests. Use c.baseURL directly
// where possible.
func (c *EmbeddingGemmaClient) baseURLString() string {
	return c.baseURL
}

// concurrencyString returns the configured concurrency.
// Package-private; used only by tests.
func (c *EmbeddingGemmaClient) concurrencyString() int {
	return c.concurrency
}

// embed POSTs the given texts to /v2/embed and decodes
// the response into a parallel slice of float32 vectors.
// Returns an error if the server returned a non-2xx
// status, the body is unreadable, or the response shape
// isn't one of the three accepted forms.
func (c *EmbeddingGemmaClient) embed(ctx context.Context, texts []string) ([][]float32, error) {
	req := EmbeddingGemmaRequest{Texts: texts}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/embed", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// No Authorization header — the /v2/embed endpoint is
	// unauthenticated by convention. Operators running an
	// auth-required EmbeddingGemma proxy can extend this
	// client; today we don't carry an API key field.

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

	out, err := decodeEmbeddingGemmaResponse(respBody, len(texts))
	if err != nil {
		return nil, err
	}

	// One-shot dimension-mismatch check: only the first
	// vector's length matters, since the server returns
	// the same dim for every input in a batch.
	if len(out) > 0 {
		c.checkDimension(len(out[0]))
	}

	return out, nil
}

// decodeEmbeddingGemmaResponse parses an /v2/embed
// response body in any of the three accepted shapes and
// returns the vectors in input order. On an unrecognized
// shape the error wraps the raw body so the operator can
// see what the server actually returned.
//
// Probe order:
//
//  1. {"embeddings": [[...], [...]]} — most common; parallel-of-texts.
//  2. {"results":     [{"embedding": [...]}]} — common wrapper form.
//  3. {"data":        [{"embedding": [...]}]} — OpenAI-style, kept for
//     servers that reuse the OpenAI response shape on /v2/embed.
//
// The probe order matters when a response happens to
// contain more than one of these fields. `embeddings` is
// preferred because that's the canonical EmbeddingGemma
// shape — if it's present we trust it.
func decodeEmbeddingGemmaResponse(body []byte, expectedCount int) ([][]float32, error) {
	// Cap the body we echo back in errors so a multi-MB
	// server error doesn't blow up logs.
	const maxBodyForError = 4096
	preview := string(body)
	if len(preview) > maxBodyForError {
		preview = preview[:maxBodyForError] + "...[truncated]"
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("embeddings API returned empty body (expected %d vectors): %s", expectedCount, preview)
	}

	var raw embeddingGemmaResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w (body: %s)", err, preview)
	}

	// Probe 1: {"embeddings": [...]} (parallel-of-texts).
	if vecsAny, ok := raw["embeddings"]; ok {
		vecs, err := decodeEmbeddingList(vecsAny, expectedCount, preview, "embeddings")
		if err != nil {
			return nil, err
		}
		return vecs, nil
	}

	// Probe 2: {"results": [{"embedding": [...]}]}.
	if resultsAny, ok := raw["results"]; ok {
		vecs, err := decodeEmbeddingObjects(resultsAny, expectedCount, preview, "results")
		if err != nil {
			return nil, err
		}
		return vecs, nil
	}

	// Probe 3: {"data": [{"embedding": [...]}]}.
	if dataAny, ok := raw["data"]; ok {
		vecs, err := decodeEmbeddingObjects(dataAny, expectedCount, preview, "data")
		if err != nil {
			return nil, err
		}
		return vecs, nil
	}

	return nil, fmt.Errorf("embeddings response missing expected fields (embeddings/results/data); body: %s", preview)
}

// decodeEmbeddingList decodes the {"embeddings": [[...],
// [...]]} shape into a slice of float32 vectors.
func decodeEmbeddingList(raw any, expectedCount int, preview, fieldName string) ([][]float32, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("embeddings field %q is not an array: %s", fieldName, preview)
	}
	if len(arr) != expectedCount {
		return nil, fmt.Errorf("embeddings API returned %d vectors for %d inputs (body: %s)", len(arr), expectedCount, preview)
	}
	out := make([][]float32, len(arr))
	for i, item := range arr {
		vec, err := decodeEmbeddingFloats(item)
		if err != nil {
			return nil, fmt.Errorf("embeddings[%d]: %w (body: %s)", i, err, preview)
		}
		out[i] = vec
	}
	return out, nil
}

// decodeEmbeddingObjects decodes the
// {"results"/"data": [{"embedding": [...]}]} shape into a
// slice of float32 vectors.
func decodeEmbeddingObjects(raw any, expectedCount int, preview, fieldName string) ([][]float32, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("embeddings field %q is not an array: %s", fieldName, preview)
	}
	if len(arr) != expectedCount {
		return nil, fmt.Errorf("embeddings API returned %d vectors for %d inputs (body: %s)", len(arr), expectedCount, preview)
	}
	out := make([][]float32, len(arr))
	for i, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("embeddings[%d] is not an object (body: %s)", i, preview)
		}
		vecAny, ok := obj["embedding"]
		if !ok {
			return nil, fmt.Errorf("embeddings[%d] is missing the 'embedding' field (body: %s)", i, preview)
		}
		vec, err := decodeEmbeddingFloats(vecAny)
		if err != nil {
			return nil, fmt.Errorf("embeddings[%d].embedding: %w (body: %s)", i, err, preview)
		}
		out[i] = vec
	}
	return out, nil
}

// decodeEmbeddingFloats decodes a single float32 vector
// from either a JSON array of numbers or a single number.
// All-numeric inputs are converted through json.Number when
// the underlying decoder used UseNumber; here we just take
// float64 since float32 round-tripping is the contract.
func decodeEmbeddingFloats(raw any) ([]float32, error) {
	switch v := raw.(type) {
	case []any:
		out := make([]float32, len(v))
		for i, item := range v {
			f, ok := item.(float64)
			if !ok {
				return nil, fmt.Errorf("element %d is not a number", i)
			}
			out[i] = float32(f)
		}
		return out, nil
	case string:
		// Some servers emit the vector as a JSON-encoded
		// string for compression. Tolerate that.
		var arr []float32
		if err := json.NewDecoder(strings.NewReader(v)).Decode(&arr); err == nil && len(arr) > 0 {
			return arr, nil
		}
		return nil, errors.New("embedding string field is not a parseable float array")
	}
	return nil, fmt.Errorf("unsupported embedding element type %T", raw)
}
