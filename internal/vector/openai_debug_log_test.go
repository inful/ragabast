package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// captureLogs redirects the global log package output to a buffer
// for the duration of fn, then restores the original writer.
// Tests use this to assert that the chat-debug logging fires
// (or stays silent) at the right times.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	buf := &bytes.Buffer{}
	oldOut := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	})
	fn()
	return buf.String()
}

// TestChatDebugLog_LogsRequestAndResponseWhenEnabled pins the
// headline behavior: when ragabast.chat.debug_log is true (i.e.
// the LLM client was constructed with logChatRequests=true)
// each chat call emits [chat-debug] request and [chat-debug]
// response lines containing the full bodies. Operators use this
// to verify the prompt template actually reaches the model
// without instrumenting the code.
func TestChatDebugLog_LogsRequestAndResponseWhenEnabled(t *testing.T) {
	var capturedRequestBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := &bytes.Buffer{}
		_, _ = buf.ReadFrom(r.Body)
		capturedRequestBody = buf.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"the answer"}}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", "", 5*time.Second, true)
	out := captureLogs(t, func() {
		_, err := client.Chat(context.Background(), []OpenAIMessage{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hello"},
		}, nil)
		require.NoError(t, err)
	})

	// The captured server-side request body must contain the system
	// prompt — that proves the prompt is reaching the wire.
	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(capturedRequestBody), &sent))
	msgs, ok := sent["messages"].([]any)
	require.True(t, ok, "request body must contain a messages array")
	found := false
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["role"] == "system" && mm["content"] == "you are a helpful assistant" {
			found = true
		}
	}
	require.True(t, found, "the system prompt must reach the wire")

	// The log output must contain both the request and the response
	// with the [chat-debug] prefix so operators can grep for them.
	require.Contains(t, out, "[chat-debug] request:")
	require.Contains(t, out, "[chat-debug] response:")
	require.Contains(t, out, "you are a helpful assistant",
		"the system prompt must appear in the chat-debug log")
	require.Contains(t, out, "the answer",
		"the assistant reply must appear in the chat-debug log")
}

// TestChatDebugLog_SilentWhenDisabled pins the safety case:
// when the flag is false (the default), no [chat-debug] lines
// are emitted. Operators who don't enable the flag must not see
// the request/response bodies in their logs.
func TestChatDebugLog_SilentWhenDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"answer"}}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", "", 5*time.Second, false)
	out := captureLogs(t, func() {
		_, err := client.Chat(context.Background(), []OpenAIMessage{
			{Role: "user", Content: "hi"},
		}, nil)
		require.NoError(t, err)
	})

	require.NotContains(t, out, "[chat-debug]",
		"no chat-debug lines must be emitted when the flag is off")
	require.NotContains(t, out, "Authorization",
		"the Authorization header must never appear in logs")
}

// TestChatDebugLog_DoesNotLeakAuthHeader pins a separate
// safety property: even with debug logging on, the Authorization
// header value (the API key) must never appear in the log output.
// A future refactor that accidentally logs the request headers
// would otherwise leak credentials.
func TestChatDebugLog_DoesNotLeakAuthHeader(t *testing.T) {
	const secret = "sk-supersecret-do-not-log"

	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", secret, 5*time.Second, true)
	out := captureLogs(t, func() {
		_, err := client.Chat(context.Background(), []OpenAIMessage{
			{Role: "user", Content: "hi"},
		}, nil)
		require.NoError(t, err)
	})

	// The server saw the header (sanity check on the test setup).
	require.Contains(t, capturedAuth, secret,
		"sanity check: the server received the Authorization header")

	// The log must NOT echo the bearer token back.
	require.NotContains(t, out, secret,
		"the bearer token must never appear in chat-debug log output")
}

// TestChatDebugLog_CapsLargeBodies guards against accidentally
// dumping multi-MB prompts into the log. Even with debug logging
// on, bodies above chatDebugLogMaxBytes are truncated with a
// marker so the log stays scannable.
func TestChatDebugLog_CapsLargeBodies(t *testing.T) {
	big := strings.Repeat("a", chatDebugLogMaxBytes+1000)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + big + `"}}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAILLMClientWithOptions(srv.URL, "m", "", 5*time.Second, true)
	out := captureLogs(t, func() {
		_, err := client.Chat(context.Background(), []OpenAIMessage{
			{Role: "user", Content: "hi"},
		}, nil)
		require.NoError(t, err)
	})

	// The full payload would be ~chatDebugLogMaxBytes+1000 bytes of
	// 'a's. The log must contain the truncation marker instead.
	require.Contains(t, out, "...[truncated]",
		"oversized chat-debug bodies must be truncated")
	require.NotContains(t, out, big,
		"the full oversized body must NOT be in the log")
}
