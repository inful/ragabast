package vector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestOpenAILLMClient_Chat_SendsTemperature(t *testing.T) {
	t.Parallel()

	errCh := make(chan error, 10)
	gotTempCh := make(chan float64, 1)
	sendErr := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		}()

		if r.Method != http.MethodPost {
			sendErr(fmt.Errorf("unexpected method: %s", r.Method))
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			sendErr(fmt.Errorf("unexpected path: %s", r.URL.Path))
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			sendErr(fmt.Errorf("read body: %w", err))
			return
		}

		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			sendErr(fmt.Errorf("unmarshal body: %w", err))
			return
		}

		v, ok := got["temperature"]
		if !ok {
			sendErr(errors.New("missing temperature"))
			return
		}
		f, ok := v.(float64)
		if !ok {
			sendErr(fmt.Errorf("temperature is not float64: %T", v))
			return
		}
		gotTempCh <- f
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "some-model", "", 5*time.Second, false)

	ctx := context.Background()
	out, err := client.Chat(ctx, []OpenAIMessage{{Role: "user", Content: "hello"}}, map[string]any{"temperature": 0.12})
	require.NoError(t, err)
	require.Equal(t, "ok", out)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	default:
	}

	select {
	case gotTemp := <-gotTempCh:
		require.InDelta(t, 0.12, gotTemp, 1e-9)
	default:
		t.Fatal("did not observe temperature in request")
	}
}

func TestOpenAILLMClient_Chat_TemperatureNotDuplicated(t *testing.T) {
	t.Parallel()

	// Server counts how many times "temperature" appears at the top level
	// of the JSON body. With the typed-field lift in place, the client
	// must send it exactly once even when temperature is in the
	// options map.
	var gotCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Naive count: occurrences of the key substring. Good enough
		// to catch a double-send regression where the typed field
		// and the options map both serialize.
		gotCount = strings.Count(string(body), `"temperature"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", "", 5*time.Second, false)
	_, err := client.Chat(context.Background(), []OpenAIMessage{{Role: "user", Content: "hi"}}, map[string]any{
		"temperature": 0.5,
		"top_p":       0.9,
	})
	require.NoError(t, err)
	require.Equal(t, 1, gotCount, "temperature must appear exactly once in the body")
}

func TestOpenAILLMClient_Chat_TemperatureCoercesFromInt(t *testing.T) {
	t.Parallel()

	// Defensive: callers that pass int instead of float64 should still
	// get a valid temperature field.
	var gotTempCh chan float64
	gotTempCh = make(chan float64, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		if err := json.Unmarshal(body, &got); err == nil {
			if v, ok := got["temperature"].(float64); ok {
				gotTempCh <- v
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", "", 5*time.Second, false)
	_, err := client.Chat(context.Background(), []OpenAIMessage{{Role: "user", Content: "hi"}}, map[string]any{
		"temperature": 1, // int, not float
	})
	require.NoError(t, err)

	select {
	case v := <-gotTempCh:
		require.InDelta(t, 1.0, v, 1e-9)
	default:
		t.Fatal("did not observe temperature")
	}
}

func TestOpenAILLMClient_Chat_SendsAuthHeader(t *testing.T) {
	t.Parallel()

	gotAuthCh := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthCh <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "some-model", "secret-key", 5*time.Second, false)

	ctx := context.Background()
	out, err := client.Chat(ctx, []OpenAIMessage{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "ok", out)

	select {
	case got := <-gotAuthCh:
		require.Equal(t, "Bearer secret-key", got)
	default:
		t.Fatal("did not observe Authorization header")
	}
}

func TestOpenAILLMClient_ChatWithSystem_PrefixesSystemMessage(t *testing.T) {
	t.Parallel()

	gotMessagesCh := make(chan []OpenAIMessage, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []OpenAIMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			gotMessagesCh <- body.Messages
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "some-model", "", 5*time.Second, false)
	ctx := context.Background()
	out, err := client.ChatWithSystem(ctx, "you are helpful", "hello", nil)
	require.NoError(t, err)
	require.Equal(t, "ok", out)

	select {
	case msgs := <-gotMessagesCh:
		require.Len(t, msgs, 2)
		require.Equal(t, "system", msgs[0].Role)
		require.Equal(t, "you are helpful", msgs[0].Content)
		require.Equal(t, "user", msgs[1].Role)
		require.Equal(t, "hello", msgs[1].Content)
	default:
		t.Fatal("did not observe request body")
	}
}

func TestOpenAILLMClient_Chat_EmptyChoicesReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "some-model", "", 5*time.Second, false)
	_, err := client.Chat(context.Background(), []OpenAIMessage{{Role: "user", Content: "x"}}, nil)
	require.ErrorIs(t, err, models.ErrGenerationFailed)
}

func TestOpenAILLMClient_Chat_EmptyContentReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"   "},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "some-model", "", 5*time.Second, false)
	_, err := client.Chat(context.Background(), []OpenAIMessage{{Role: "user", Content: "x"}}, nil)
	require.ErrorIs(t, err, models.ErrGenerationFailed)
}

func TestOpenAILLMClient_Chat_EmptyMessagesReturnsError(t *testing.T) {
	t.Parallel()

	client := NewOpenAILLMClientWithOptions("http://example.invalid", "m", "", time.Second, false)
	_, err := client.Chat(context.Background(), nil, nil)
	require.ErrorIs(t, err, models.ErrGenerationFailed)
}

func TestOpenAILLMClient_Chat_ServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", "", 5*time.Second, false)
	_, err := client.Chat(context.Background(), []OpenAIMessage{{Role: "user", Content: "x"}}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestOpenAILLMClient_Chat_ApiErrorInBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"context length exceeded","type":"invalid_request_error","code":"context_length_exceeded"},"choices":[]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", "", 5*time.Second, false)
	_, err := client.Chat(context.Background(), []OpenAIMessage{{Role: "user", Content: "x"}}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context length exceeded")
}
