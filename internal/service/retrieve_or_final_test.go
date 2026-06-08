package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

type ollamaTestServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls int
	mode  string // "retrieve" or "final"
}

func newOllamaTestServer(t *testing.T, mode string) *ollamaTestServer {
	t.Helper()
	h := &ollamaTestServer{mode: mode}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embeddings":
			var req struct {
				Prompt string `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
			_ = r.Body.Close()

			p := req.Prompt
			vec := []float32{0, 0, 1}
			switch {
			case strings.Contains(p, "DOC1") || strings.TrimSpace(p) == "q1":
				vec = []float32{1, 0, 0}
			case strings.Contains(p, "DOC2") || strings.TrimSpace(p) == "q2":
				vec = []float32{0, 1, 0}
			}

			resp := map[string]any{"embedding": vec}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
			return
		case "/api/generate":
			h.mu.Lock()
			h.calls++
			callNum := h.calls
			h.mu.Unlock()

			var req struct {
				Prompt string `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
			_ = r.Body.Close()

			var out string
			if callNum == 1 {
				if h.mode == "final" {
					out = `{"action":"final","answer":"Final answer."}`
				} else {
					out = `{"action":"retrieve","queries":["q2"],"top_k":1}`
				}
			} else {
				out = "Answer."
			}

			resp := map[string]any{"response": out, "done": true}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(err.Error()))
				return
			}
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return h
}

func (h *ollamaTestServer) Close() { h.srv.Close() }

func (h *ollamaTestServer) URL() string { return h.srv.URL }

func (h *ollamaTestServer) Calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func TestQueryDebugWithOptions_RetrieveOrFinalLoop_TwoHopAddsSources(t *testing.T) {
	h := newOllamaTestServer(t, "retrieve")
	defer h.Close()

	cfg := config.DefaultConfig()
	cfg.Ollama.BaseURL = h.URL()
	cfg.Ollama.EnableThinking = true
	cfg.Ollama.Timeout = 5 * time.Second
	cfg.VectorDB.PersistenceDir = t.TempDir()
	cfg.VectorDB.CollectionName = "ragabast-test"
	cfg.VectorDB.EmbeddingDimension = 3
	cfg.Processing.MaxChunkSize = 5000
	cfg.Processing.MinChunkSize = 1

	svc, err := NewService(cfg)
	require.NoError(t, err)

	ctx := context.Background()

	doc1 := "---\nuid: doc-1\nurls:\n  - https://example.com/doc1\n---\n# Doc 1\nDOC1\n"
	doc2 := "---\nuid: doc-2\nurls:\n  - https://example.com/doc2\n---\n# Doc 2\nDOC2\n"
	_, err = svc.IngestDocument(ctx, doc1)
	require.NoError(t, err)
	_, err = svc.IngestDocument(ctx, doc2)
	require.NoError(t, err)

	answer, debug, err := svc.QueryDebugWithOptions(ctx, "q1", 1, LLMOptions{})
	require.NoError(t, err)
	require.NotNil(t, debug)

	// Two LLM calls: decision + final.
	require.Equal(t, 2, h.Calls())

	// Ensure the second-hop document was included via links enforcement.
	require.Contains(t, answer, "https://example.com/doc1")
	require.Contains(t, answer, "https://example.com/doc2")
	require.GreaterOrEqual(t, len(debug.Results), 2)
}

func TestQueryDebugWithOptions_RetrieveOrFinalLoop_FinalSkipsSecondHop(t *testing.T) {
	h := newOllamaTestServer(t, "final")
	defer h.Close()

	cfg := config.DefaultConfig()
	cfg.Ollama.BaseURL = h.URL()
	cfg.Ollama.EnableThinking = true
	cfg.Ollama.Timeout = 5 * time.Second
	cfg.VectorDB.PersistenceDir = t.TempDir()
	cfg.VectorDB.CollectionName = "ragabast-test"
	cfg.VectorDB.EmbeddingDimension = 3
	cfg.Processing.MaxChunkSize = 5000
	cfg.Processing.MinChunkSize = 1

	svc, err := NewService(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	doc1 := "---\nuid: doc-1\nurls:\n  - https://example.com/doc1\n---\n# Doc 1\nDOC1\n"
	_, err = svc.IngestDocument(ctx, doc1)
	require.NoError(t, err)

	answer, _, err := svc.QueryDebugWithOptions(ctx, "q1", 1, LLMOptions{})
	require.NoError(t, err)

	// One LLM call for decision; it contains the final answer.
	require.Equal(t, 1, h.Calls())
	require.Contains(t, answer, "Final answer.")
	require.Contains(t, answer, "https://example.com/doc1")
}
