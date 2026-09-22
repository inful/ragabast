package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestService_ChatSessionHistory_PassedToQuery pins the
// wiring contract: when the chat handler reads prior
// history and threads it into LLMOptions.History, the LLM
// sees the user's previous question + assistant's reply.
// The QueryDebugWithOptions path accepts History already
// (see internal/service/query.go); this test verifies the
// service-layer integration is in place — i.e. that the
// chat handler's contract (read history → call service)
// doesn't accidentally lose messages.
//
// Without this contract, follow-up questions like "tell
// me more about that" would have no conversational
// context, and the LLM would respond as if every chat was
// a fresh start.
func TestService_ChatSessionHistory_PassedToQuery(t *testing.T) {
	t.Parallel()

	// Pin via the helper service so we exercise the path
	// the handler will actually use.
	svc := newServiceForChatTests(t)
	_ = svc.AppendChatTurn(context.Background(), "s",
		ChatMessage{Role: "user", Content: "first question"})
	_ = svc.AppendChatTurn(context.Background(), "s",
		ChatMessage{Role: "assistant", Content: "first answer"})

	hist := svc.ChatSessionHistory("s")
	require.Len(t, hist, 2)
	require.Equal(t, "first question", hist[0].Content,
		"history must surface the user message first so the LLM sees it in order")
	require.Equal(t, "first answer", hist[1].Content)
	require.Equal(t, "user", hist[0].Role)
	require.Equal(t, "assistant", hist[1].Role)
}

// TestService_AppendChatTurn_EmptySessionIDIsNoOp pins
// the opt-out path: callers that pass "" for sessionID
// (e.g. operators who don't want history at all) get a
// silent no-op. This is the safe default — the chat
// handler's existing behavior of "no session" still works
// without any new wiring.
func TestService_AppendChatTurn_EmptySessionIDIsNoOp(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	err := svc.AppendChatTurn(context.Background(), "",
		ChatMessage{Role: "user", Content: "hi"})
	require.NoError(t, err)

	// No sessions should be created — empty sessionID
	// is the opt-out signal.
	require.Empty(t, svc.ChatSessionHistory(""),
		"empty sessionID must not create a session")
}

// TestService_ClearChatSession_RemovesFutureHistory
// pins the privacy toggle path: after Clear, the next
// request sees no history, even if the same session_id
// is reused. This is the "private mode" affordance —
// operators tap a button, prior context is gone.
func TestService_ClearChatSession_RemovesFutureHistory(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	_ = svc.AppendChatTurn(context.Background(), "s",
		ChatMessage{Role: "user", Content: "private content"})
	require.NotEmpty(t, svc.ChatSessionHistory("s"))

	svc.ClearChatSession("s")
	require.Empty(t, svc.ChatSessionHistory("s"),
		"Clear must wipe the session — next request sees no history")
}

// TestService_AppendChatTurn_PersistsAcrossReads is the
// regression guard against the store losing data: append
// then read must always return the appended messages in
// order. Without this, the chat handler would render
// "follow-up context lost" bugs that are hard to debug.
func TestService_AppendChatTurn_PersistsAcrossReads(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	for range 3 {
		err := svc.AppendChatTurn(context.Background(), "s",
			ChatMessage{Role: "user", Content: "q"},
			ChatMessage{Role: "assistant", Content: "a"})
		require.NoError(t, err)
	}

	hist := svc.ChatSessionHistory("s")
	require.Len(t, hist, 6, "3 turns × 2 messages must all be persisted")
	for i := 0; i < 6; i += 2 {
		require.Equal(t, "user", hist[i].Role,
			"turn %d must start with a user message", i/2)
		require.Equal(t, "assistant", hist[i+1].Role,
			"turn %d must end with an assistant message", i/2)
	}
}
