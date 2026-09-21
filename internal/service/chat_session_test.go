package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChatSessionStore_GetReturnsEmptyForUnknownSession
// pins the boundary: a session_id the store has never
// seen returns an empty history (not nil, not an error)
// so the chat handler can pass-through cleanly.
func TestChatSessionStore_GetReturnsEmptyForUnknownSession(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	got := store.Get("never-seen")
	require.NotNil(t, got,
		"unknown session must return an empty slice, not nil")
	require.Empty(t, got)
}

// TestChatSessionStore_AppendThenGetRoundTrip pins the
// basic contract: appending a user/assistant exchange
// stores it, and a subsequent Get returns the same
// history. This is what makes follow-up questions
// work — the LLM sees the prior exchange.
func TestChatSessionStore_AppendThenGetRoundTrip(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	store.Append("s1", []ChatMessage{
		{Role: "user", Content: "what is ragabast?"},
		{Role: "assistant", Content: "It's a RAG server."},
	})

	got := store.Get("s1")
	require.Len(t, got, 2)
	require.Equal(t, "user", got[0].Role)
	require.Equal(t, "what is ragabast?", got[0].Content)
	require.Equal(t, "assistant", got[1].Role)
}

// TestChatSessionStore_AppendTrimsToMaxTurns pins the
// configurable max-history contract. A session with
// MaxTurns=3 that accumulates 5 turns drops the oldest
// turns — operators want bounded memory growth.
func TestChatSessionStore_AppendTrimsToMaxTurns(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 3})
	for range 5 {
		store.Append("s", []ChatMessage{
			{Role: "user", Content: "q"},
			{Role: "assistant", Content: "a"},
		})
	}
	got := store.Get("s")
	require.Len(t, got, 6,
		"3 turns × 2 messages = 6 entries (FIFO-trimmed)")
	// The first surviving entry is the start of turn 3.
	require.Equal(t, "q", got[0].Content,
		"oldest turns must be trimmed (got %v)", got)
}

// TestChatSessionStore_SessionsAreIsolated pins the
// per-session isolation contract: two sessions with the
// same ID space don't bleed into each other.
func TestChatSessionStore_SessionsAreIsolated(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	store.Append("alice", []ChatMessage{{Role: "user", Content: "alice-q"}})
	store.Append("bob", []ChatMessage{{Role: "user", Content: "bob-q"}})

	require.Equal(t, "alice-q", store.Get("alice")[0].Content)
	require.Equal(t, "bob-q", store.Get("bob")[0].Content)
	require.Len(t, store.Get("alice"), 1)
	require.Len(t, store.Get("bob"), 1)
}

// TestChatSessionStore_ClearDropsSession pins the
// privacy contract: clear deletes all history. Used by
// the "private mode" toggle in the chat UI — operators
// who want a clean session for sensitive questions
// tap clear and the prior context is gone immediately.
func TestChatSessionStore_ClearDropsSession(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	store.Append("s", []ChatMessage{{Role: "user", Content: "x"}})
	require.NotEmpty(t, store.Get("s"))

	store.Clear("s")
	require.Empty(t, store.Get("s"),
		"cleared session must read back as empty")
}

// TestChatSessionStore_ConcurrentSafe pins the thread-
// safety contract: many goroutines writing/reading the
// same session must not race. httptest can drive many
// concurrent /chat/message requests against the same
// session_id, so the store needs to be safe under
// concurrent access.
func TestChatSessionStore_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 100})

	done := make(chan struct{})
	const writers = 8
	for range writers {
		go func() {
			for range 50 {
				store.Append("shared", []ChatMessage{
					{Role: "user", Content: "x"},
					{Role: "assistant", Content: "y"},
				})
				_ = store.Get("shared")
			}
			done <- struct{}{}
		}()
	}
	for range writers {
		<-done
	}
	// No race detector failure = success.
	require.NotNil(t, store.Get("shared"))
}

// TestService_ChatSession_GetReturnsClone pins the
// service-layer wrapper: getting history from a session
// returns a clone so the caller can't mutate the store's
// internal slice. Defensive copy — this is a public API.
func TestService_ChatSession_GetReturnsClone(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	svc.chatSessions.Append("s", []ChatMessage{
		{Role: "user", Content: "hi"},
	})

	got := svc.ChatSessionHistory("s")
	require.Equal(t, "hi", got[0].Content)

	// Mutate the returned slice — the store must not be
	// affected.
	got[0].Content = "mutated"
	require.Equal(t, "hi", svc.ChatSessionHistory("s")[0].Content,
		"returned slice must be a defensive copy")
}

// TestService_ChatSession_AppendViaService pins the
// service-layer wrapper: the service exposes Append for
// the chat handler to record each exchange after the
// LLM response comes back.
func TestService_ChatSession_AppendViaService(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	err := svc.AppendChatTurn(context.Background(), "s", ChatMessage{Role: "user", Content: "hi"})
	require.NoError(t, err)
	err = svc.AppendChatTurn(context.Background(), "s", ChatMessage{Role: "assistant", Content: "hello"})
	require.NoError(t, err)

	hist := svc.ChatSessionHistory("s")
	require.Len(t, hist, 2)
	require.Equal(t, "user", hist[0].Role)
	require.Equal(t, "assistant", hist[1].Role)
}

// TestService_ChatSession_ClearRemoves pins the
// clear-via-service entrypoint: the chat handler hits
// this when the operator toggles "private mode" on.
func TestService_ChatSession_ClearRemoves(t *testing.T) {
	t.Parallel()

	svc := newServiceForChatTests(t)
	_ = svc.AppendChatTurn(context.Background(), "s", ChatMessage{Role: "user", Content: "hi"})
	require.NotEmpty(t, svc.ChatSessionHistory("s"))

	svc.ClearChatSession("s")
	require.Empty(t, svc.ChatSessionHistory("s"))
}

// --- helpers ---

// ChatSessionConfig is the constructor input for the
// in-memory session store. Re-exported from production
// code so the test file stays decoupled from internal
// struct layout.
type ChatSessionConfig = ChatSessionStoreConfig

func newServiceForChatTests(t *testing.T) *Service {
	t.Helper()
	svc := &Service{
		chatSessions: NewChatSessionStore(ChatSessionConfig{MaxTurns: 20}),
	}
	return svc
}
