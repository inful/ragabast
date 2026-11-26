package vector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChatTimeout_NamesTheTimeoutAndTheKnob pins the diagnostics
// contract for the chat timeout: the error must say how long
// ragabast waited and which knob controls it.
//
// A deployment hit this: the chat gateway loaded the model per
// request, so every request timed out. The Go error alone
// ("context deadline exceeded (Client.Timeout exceeded while
// awaiting headers)") named neither the wait nor the knob.
func TestChatTimeout_NamesTheTimeoutAndTheKnob(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep past the client's timeout so the request is
		// abandoned before the server answers.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "coding-agent", "", 150*time.Millisecond, false)

	_, err := client.ChatWithSystem(context.Background(), "sys", "hello", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ollama.timeout",
		"the error must name the knob that controls the wait")
	assert.Contains(t, err.Error(), "150ms",
		"the error must say how long ragabast waited")
}

// TestChatTimeout_NoHintForOtherErrors pins that the hint stays out
// of the way when the failure is not a timeout.
func TestChatTimeout_NoHintForOtherErrors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", "", 5*time.Second, false)

	_, err := client.ChatWithSystem(context.Background(), "sys", "hello", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "ollama.timeout",
		"a non-timeout failure must not carry the timeout hint")
}
