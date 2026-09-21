package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/require"
)

// TestService_QueryDebugWithOptions_EnrichesResultsWithDocbuilderURL
// pins the fix for the user's 2026-09-20 bug report: chat
// replies showed source titles as plain text instead of
// clickable links. Root cause was that QueryDebugWithOptions
// called s.vectorOps.Search(...) directly and returned the raw
// results in QueryDebugInfo.Results — it never called
// s.enrichWithDocbuilderURLs(results), so DocbuilderURL was
// empty on every result, and InlineSourceLinks fell into the
// "no URL → bare text" branch.
//
// The test stands up an httptest embeddings server, ingests a
// test chunk with a UID, stubs the LLM client, and asserts
// that the results returned by QueryDebugWithOptions have
// DocbuilderURL populated. The assertion fails on the buggy
// code (DocbuilderURL is "") and passes after the fix is
// applied (DocbuilderURL points at the configured
// docbuilder_base_url + /_uid/<uid>/).
func TestService_QueryDebugWithOptions_EnrichesResultsWithDocbuilderURL(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	const testDim = 768

	// Embeddings server: always returns testDim floats for any
	// input. Handles /v1/models for ValidateConnection's
	// reachability probe.
	embSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models", "/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/embeddings":
			vec := make([]float32, testDim)
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"object":"list","data":[{"object":"embedding","index":0,"embedding":%s}]}`,
				encodeF32Slice(vec),
			)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(embSrv.Close)

	cfg := config.DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "https://docs.example.com"
	cfg.Ollama.BaseURL = embSrv.URL
	cfg.Ollama.ChatBaseURL = embSrv.URL // unused — llmClient is stubbed
	cfg.Ollama.EmbeddingModel = "nomic-embed-text:v1.5"
	cfg.VectorDB.EmbeddingDimension = testDim
	cfg.VectorDB.PersistenceDir = tmp

	db, err := vector.NewVectorDB("test", testDim, tmp, "test-model")
	require.NoError(t, err)

	embeddings := vector.NewOpenAIEmbeddingClientWithOptions(
		cfg.Ollama.BaseURL,
		cfg.Ollama.EmbeddingModel,
		cfg.Ollama.EffectiveEmbeddingAPIKey(),
		cfg.Ollama.Timeout,
		cfg.Ollama.EmbeddingDimensions,
		cfg.Ollama.EmbeddingConcurrency,
	)
	vectorOps := vector.NewVectorOperations(db, embeddings)

	// Ingest a chunk with a UID. Without the fix,
	// docBuilderURL on the SearchResult would be "".
	chunk := &models.Chunk{
		ID:                 "c1",
		DocumentID:         "d1",
		DocumentTitle:      "Comprehensive Architecture Documentation",
		UID:                "comprehensive-architecture",
		HeaderPath:         "H1",
		Level:              1,
		StartLine:          1,
		EndLine:            1,
		Content:            "DocBuilder is a Go CLI tool with structured logging.",
		Fingerprint:        "fp-1",
		DocumentURLs:       []string{},
		DocumentTags:       []string{},
		DocumentCategories: []string{},
	}
	chunkEmbedding, err := embeddings.GenerateEmbedding(t.Context(), "DocBuilder is a Go CLI tool")
	require.NoError(t, err)
	require.NoError(t, db.AddChunk(t.Context(), chunk, chunkEmbedding))

	// Stub the LLM client so QueryDebugWithOptions returns the
	// user's actual reply (the one from the 2026-09-20 chat
	// debug log) without needing a real LLM endpoint.
	llmStub := &fakeLLMClient{
		reply: "DocBuilder is a **Go** CLI tool [src:0].",
	}

	svc := &Service{
		config:    cfg,
		vectorOps: vectorOps,
		llmClient: llmStub,
	}

	_, info, err := svc.QueryDebugWithOptions(t.Context(), "What is DocBuilder?", 5, LLMOptions{})
	require.NoError(t, err)
	require.NotNil(t, info, "QueryDebugWithOptions must return debug info")
	require.Len(t, info.Results, 1, "expected one retrieved result")

	// Headline assertion: the result handed back to the
	// handler has DocbuilderURL populated. Without the fix,
	// this is "" and the post-processor falls into the
	// bare-text fallback the user has been seeing.
	require.Equal(t,
		"https://docs.example.com/_uid/comprehensive-architecture/",
		info.Results[0].DocbuilderURL,
		"QueryDebugWithOptions must populate DocbuilderURL on every result so InlineSourceLinks can wrap [src:N] in clickable links")
}

// fakeLLMClient is a stub llmChatClient that returns a fixed
// reply regardless of input. Used by tests of QueryDebugWithOptions
// to pin the post-processor contract without standing up a real
// LLM endpoint.
type fakeLLMClient struct {
	reply string
	err   error
}

func (f *fakeLLMClient) Chat(_ context.Context, _ []vector.OpenAIMessage, _ map[string]any) (string, error) {
	return f.reply, f.err
}

func (f *fakeLLMClient) ChatWithSystem(_ context.Context, _, _ string, _ map[string]any) (string, error) {
	return f.reply, f.err
}

// encodeF32Slice renders a []float32 as the JSON array form
// the embeddings endpoint uses. Mirrors the helper in
// cmd/doctor_test.go — kept local to avoid pulling a test-only
// dependency across the cmd→service boundary.
func encodeF32Slice(v []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%g", f))
	}
	sb.WriteByte(']')
	return sb.String()
}
