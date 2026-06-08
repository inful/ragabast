package vector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{"http://host:8000", "http://host:8000"},
		{"http://host:8000/", "http://host:8000"},
		{"http://host:8000/v1", "http://host:8000"},
		{"http://host:8000/v1/", "http://host:8000"},
		{"https://api.openai.com/v1", "https://api.openai.com"},
		{"https://api.openai.com/v1/", "https://api.openai.com"},
		{"http://127.0.0.1:8000/v1", "http://127.0.0.1:8000"},
		{"http://127.0.0.1:8000/V1", "http://127.0.0.1:8000"},
		// Long paths are preserved when there's no /v1 suffix.
		{"http://host:8000/openai", "http://host:8000/openai"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := normalizeOpenAIBaseURL(tc.in)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestOpenAILLMClient_AcceptsBaseURLWithV1Suffix(t *testing.T) {
	t.Parallel()

	// The server returns /v1/chat/completions when called at /v1/v1/chat/completions
	// is *not* what we want. With normalizeOpenAIBaseURL stripping /v1, the
	// client posts to /v1/chat/completions even when the configured URL is
	// "http://127.0.0.1:8000/v1".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	// Re-add /v1 to whatever the test server gave us. If the helper doesn't
	// strip it, the client would post to /v1/v1/chat/completions.
	client := NewOpenAILLMClientWithOptions(srv.URL+"/v1", "m", "", 5*time.Second)
	out, err := client.Chat(context.Background(), []OpenAIMessage{{Role: "user", Content: "x"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "ok", out)
}

func TestOpenAIEmbeddingClient_AcceptsBaseURLWithV1Suffix(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("expected /v1/embeddings, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL+"/v1", "m", "", 5*time.Second)
	vec, err := client.GenerateEmbedding(context.Background(), "x")
	require.NoError(t, err)
	require.Equal(t, []float32{0.1}, vec)
}
