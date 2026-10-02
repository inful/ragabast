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

// TestChatSessionStore_GetSourceKinds_EmptyForUnknownSession
// pins the boundary contract for the new SourceKinds field:
// a session_id the store has never seen must return nil
// (not an empty slice — that distinction matters for the
// chat handler, which uses nil as "no default to pre-fill").
func TestChatSessionStore_GetSourceKinds_EmptyForUnknownSession(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	got := store.GetSourceKinds("never-seen")
	require.Nil(t, got,
		"unknown session must return nil for source_kinds — distinguishes 'no default' from 'empty selection'")
}

// TestChatSessionStore_SetSourceKinds_RoundTrip pins the basic
// contract: setting then getting returns the same slice (in
// order). The slice stored is a defensive copy so a caller that
// mutates its local slice doesn't poison the store.
func TestChatSessionStore_SetSourceKinds_RoundTrip(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	store.SetSourceKinds("s1", []string{"gitlab", "docbuilder"})

	got := store.GetSourceKinds("s1")
	require.Equal(t, []string{"gitlab", "docbuilder"}, got,
		"set then get must round-trip the same slice in order")
}

// TestChatSessionStore_SetSourceKinds_ReturnsDefensiveCopy pins
// that the returned slice is independent of the caller's
// subsequent mutation. Without this, a chat handler that
// renders the slice into a template (or appends to it locally)
// could silently corrupt the store's view.
func TestChatSessionStore_SetSourceKinds_ReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	original := []string{"gitlab"}
	store.SetSourceKinds("s1", original)
	original[0] = "mutated"

	got := store.GetSourceKinds("s1")
	require.Equal(t, []string{"gitlab"}, got,
		"mutating the caller's slice after Set must not change the store's view")
}

// TestChatSessionStore_SourceKindsIndependentOfHistory pins that
// the source_kinds default and the conversation history are
// stored as separate concerns. Setting one must not affect the
// other — a follow-up question with a cleared selection still
// has its history preserved.
func TestChatSessionStore_SourceKindsIndependentOfHistory(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	store.Append("s1", []ChatMessage{{Role: "user", Content: "q"}})
	store.SetSourceKinds("s1", []string{"gitlab"})

	require.Len(t, store.Get("s1"), 1,
		"history must survive a source_kinds set on the same session")
	require.Equal(t, []string{"gitlab"}, store.GetSourceKinds("s1"),
		"source_kinds must survive a history append on the same session")
}

// TestChatSessionStore_Clear_AlsoClearsSourceKinds pins that
// the "private mode" / clear-session UI affordance wipes both
// the conversation history AND the source-kind default. After
// clear, the form must pre-fill with "no default" — otherwise
// the operator would see a fresh chat that's mysteriously
// scoped to gitlab issues.
func TestChatSessionStore_Clear_AlsoClearsSourceKinds(t *testing.T) {
	t.Parallel()

	store := NewChatSessionStore(ChatSessionConfig{MaxTurns: 10})
	store.Append("s1", []ChatMessage{{Role: "user", Content: "q"}})
	store.SetSourceKinds("s1", []string{"gitlab"})

	store.Clear("s1")

	require.Empty(t, store.Get("s1"), "Clear must drop history")
	require.Nil(t, store.GetSourceKinds("s1"),
		"Clear must drop the source_kinds default — fresh chat starts with no scope")
}

// TestService_ChatSession_SetSourceKindsViaService pins the
// service-layer wrapper. The chat handler goes through the
// Service (not the store directly) so the wrapper is what
// commits 3+ will exercise.
func TestService_ChatSession_SetSourceKindsViaService(t *testing.T) {
	t.Parallel()

	svc := &Service{
		chatSessions: NewChatSessionStore(ChatSessionConfig{MaxTurns: 10}),
	}
	svc.SetChatSessionSourceKinds("s1", []string{"docbuilder"})

	require.Equal(t, []string{"docbuilder"}, svc.ChatSessionSourceKinds("s1"))
}

// TestService_ChatSession_NilStoreIsNoop pins the defensive
// contract: a Service constructed without a session store
// (older callers, test fakes) must not nil-panic on a
// SetChatSessionSourceKinds call. Mirrors the existing
// AppendChatTurn / ChatSessionHistory nil-safe pattern.
func TestService_ChatSession_NilStoreIsNoop(t *testing.T) {
	t.Parallel()

	svc := &Service{} // no chatSessions
	require.NotPanics(t, func() {
		svc.SetChatSessionSourceKinds("s1", []string{"gitlab"})
	})
	require.Nil(t, svc.ChatSessionSourceKinds("s1"),
		"nil store must return nil for source_kinds")
}

// TestService_ChatSession_SetSourceKindsEmptyStringNoop pins the
// "sticky default" rule: setting an empty slice (or one with
// only empty strings) must NOT clear the existing default. The
// chat handler only calls SetSourceKinds on a non-empty form
// submission; this test pins that contract at the store layer.
func TestService_ChatSession_SetSourceKindsEmptyStringNoop(t *testing.T) {
	t.Parallel()

	svc := &Service{
		chatSessions: NewChatSessionStore(ChatSessionConfig{MaxTurns: 10}),
	}
	svc.SetChatSessionSourceKinds("s1", []string{"gitlab"})
	// Operator clears the form — empty submission.
	svc.SetChatSessionSourceKinds("s1", []string{})

	require.Equal(t, []string{"gitlab"}, svc.ChatSessionSourceKinds("s1"),
		"empty Set must NOT clear the existing default — sticky-default contract")
}
