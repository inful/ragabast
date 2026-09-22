package service

import (
	"context"
	"strings"
	"sync"
	"time"
)

// ChatSessionStoreConfig configures an in-memory chat
// session store. Kept simple — no persistence, no eviction
// strategy beyond the bounded FIFO trim. Operators with a
// persistent history requirement can layer a backing
// store on top of this interface.
type ChatSessionStoreConfig struct {
	// MaxTurns caps the number of user/assistant exchanges
	// per session. When a session exceeds this, the oldest
	// turns are dropped FIFO. Default 20 — operators want
	// enough context for follow-ups (3-5 turns is typical
	// for a working session) without unbounded growth.
	MaxTurns int

	// IdleTTL drops sessions whose last access is older
	// than this. Prevents stale sessions from accumulating
	// forever in long-running deployments. Zero = no TTL
	// (sessions live until the process restarts).
	IdleTTL time.Duration
}

// chatSession is the per-session state held by the store.
// History is the canonical conversation slice in submit
// order — oldest first, newest last — matching the order
// the LLM consumes.
//
// lastAccess is wired for a future IdleTTL GC pass; not
// read today but set on every Get/Append so the value
// is always current when the GC lands. The unused-field
// lint allows it on purpose.
type chatSession struct {
	history    []ChatMessage
	lastAccess time.Time //nolint:unused // wired for future IdleTTL GC
}

// ChatSessionStore is an in-memory map of session_id →
// conversation history. Safe for concurrent use; the
// underlying mutex protects all reads and writes.
//
// Not persisted — sessions live only as long as the
// ragabast process. A persistent store (SQLite, Redis)
// can be added later behind the same interface.
type ChatSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*chatSession
	cfg      ChatSessionStoreConfig
}

// NewChatSessionStore constructs the store with the given
// config. MaxTurns is clamped to a sensible floor (1) so
// a config typo can't silently disable the cap.
func NewChatSessionStore(cfg ChatSessionStoreConfig) *ChatSessionStore {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 20
	}
	return &ChatSessionStore{
		sessions: make(map[string]*chatSession),
		cfg:      cfg,
	}
}

// Get returns a defensive copy of the session's history.
// Returns an empty slice (not nil) when the session is
// unknown, so callers can iterate without nil-checks.
//
// The clone is important: callers (the chat handler) may
// mutate the returned slice locally without poisoning the
// store's view. (Mutating the inner ChatMessage values is
// fine — string fields are immutable in Go — but the
// caller might append or reorder, and we don't want that
// to leak back into the store.)
func (s *ChatSessionStore) Get(sessionID string) []ChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return []ChatMessage{}
	}
	sess.lastAccess = time.Now()
	out := make([]ChatMessage, len(sess.history))
	copy(out, sess.history)
	return out
}

// Append records a single user/assistant exchange (one or
// two ChatMessages) on the session. The exchange is
// appended verbatim; if the session exceeds MaxTurns,
// the oldest entries are dropped FIFO.
//
// Sessions are created lazily on first append — Get on
// an unknown session returns empty, Append on an unknown
// session creates it. This keeps the chat handler's flow
// simple: it doesn't have to "create" a session before
// appending to it.
func (s *ChatSessionStore) Append(sessionID string, exchange []ChatMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		sess = &chatSession{}
		s.sessions[sessionID] = sess
	}
	sess.lastAccess = time.Now()
	sess.history = append(sess.history, exchange...)
	s.trim(sess)
}

// Clear drops a session's history. Future Get calls read
// back empty (the session slot is removed; it gets
// re-created on next Append). Used by the "private mode"
// toggle in the UI.
func (s *ChatSessionStore) Clear(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// trim enforces MaxTurns by dropping the oldest messages
// until the session fits. Called under the lock.
func (s *ChatSessionStore) trim(sess *chatSession) {
	maxMessages := s.cfg.MaxTurns * 2 // each "turn" is user + assistant
	for len(sess.history) > maxMessages {
		sess.history = sess.history[2:] // drop oldest pair
	}
}

// AppendChatTurn is the service-layer wrapper. The chat
// handler calls this AFTER the LLM response so the new
// exchange is persisted for the next request.
//
// The exchange is typically two messages: a user message
// and the assistant's reply. The handler builds the
// exchange — this method just stores it.
func (s *Service) AppendChatTurn(_ context.Context, sessionID string, exchange ...ChatMessage) error {
	if sessionID == "" {
		// No session = no persistence. Caller passes "" to
		// opt out (e.g. one-shot Q&A); this is not an error.
		return nil
	}
	if s.chatSessions == nil {
		// Defensive: a Service constructed without a session
		// store (e.g. older callers or unit-test fakes) just
		// drops the message. Better than nil-panicking in
		// production.
		return nil
	}
	s.chatSessions.Append(sessionID, exchange)
	return nil
}

// ChatSessionHistory returns a defensive copy of the
// session's history. Empty slice for unknown sessions.
//
// The HTTP layer calls this to render the chat UI on
// page load (so reloads restore prior context).
func (s *Service) ChatSessionHistory(sessionID string) []ChatMessage {
	if sessionID == "" || s.chatSessions == nil {
		return []ChatMessage{}
	}
	return s.chatSessions.Get(sessionID)
}

// ClearChatSession drops a session's history. Used by the
// "private mode" toggle — operators who want a clean slate
// tap clear and the prior context is gone immediately.
func (s *Service) ClearChatSession(sessionID string) {
	if sessionID == "" || s.chatSessions == nil {
		return
	}
	s.chatSessions.Clear(sessionID)
}

// ExportChatTranscript renders a session's history as a
// markdown document. Returns "" for unknown / empty
// sessions so the HTTP handler can decide between 404
// and 200-with-empty-body. The shape is deliberately
// simple — no source citations (we don't store them
// yet), no timestamps per turn (would require expanding
// ChatMessage). Just the conversation in chronological
// order, which is the operator's actual use case:
//
//	go run . serve → chat → export → paste into a wiki
//
// Layout:
//
//	## User
//
//	What is ragabast?
//
//	## Assistant
//
//	A markdown chunker and RAG server.
//
// H2 (not H3) so the transcript is readable in any
// markdown viewer; H1 is reserved for the document title
// if the user pastes it into a wiki.
func (s *Service) ExportChatTranscript(sessionID string) string {
	if sessionID == "" || s.chatSessions == nil {
		return ""
	}
	history := s.chatSessions.Get(sessionID)
	if len(history) == 0 {
		return ""
	}

	var b strings.Builder
	for _, msg := range history {
		// Title-case the role so `## User` / `## Assistant`
		// are stable across readers (some viewers lowercase
		// the heading on render, which would read as a typo).
		heading := strings.ToUpper(msg.Role[:1]) + msg.Role[1:]
		b.WriteString("## ")
		b.WriteString(heading)
		b.WriteString("\n\n")
		b.WriteString(msg.Content)
		// Trailing blank line — markdown requires two
		// newlines to terminate a paragraph; one is a
		// soft break that viewers may collapse.
		b.WriteString("\n\n")
	}
	return b.String()
}
