package service

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChatSessionStore_GCRemovesIdleSessions pins the
// audit finding 1 behavior: sessions whose lastAccess is
// older than IdleTTL are removed by the background GC.
func TestChatSessionStore_GCRemovesIdleSessions(t *testing.T) {
	store := NewChatSessionStore(ChatSessionStoreConfig{
		MaxTurns: 5,
		IdleTTL:  10 * time.Millisecond,
	})
	t.Cleanup(store.Stop)

	store.Append("alice", []ChatMessage{{Role: "user", Content: "hi"}})
	require.NotEmpty(t, store.Get("alice"), "fresh session must be readable")

	// Wait past the IdleTTL plus a margin for the GC's
	// next sweep tick. The ticker fires at minute-aligned
	// wall-clock boundaries; for tests we expose gc() as
	// the synchronous entry point.
	time.Sleep(20 * time.Millisecond)
	store.gc()

	assert.Empty(t, store.Get("alice"), "session older than IdleTTL must be evicted")
}

// TestChatSessionStore_GCSkippedWhenIdleTTLZero pins the
// historical behavior — IdleTTL=0 means sessions live until
// process restart. Important so operators in setups with
// an external "private mode" toggle can opt out without
// surprising their users.
func TestChatSessionStore_GCSkippedWhenIdleTTLZero(t *testing.T) {
	store := NewChatSessionStore(ChatSessionStoreConfig{
		MaxTurns: 5,
		IdleTTL:  0,
	})
	// GC goroutine isn't started when IdleTTL=0; Stop
	// must still be safe to call.
	t.Cleanup(store.Stop)

	store.Append("alice", []ChatMessage{{Role: "user", Content: "hi"}})

	// Force a "stale lastAccess" manually so we can
	// confirm gc() with IdleTTL=0 is a no-op even on a
	// fully-stale map.
	time.Sleep(5 * time.Millisecond)
	store.gc()

	assert.NotEmpty(t, store.Get("alice"), "IdleTTL=0 must NOT evict")
}

// TestChatSessionStore_StopIdempotent pins that Stop can
// be called twice without blocking or panicking — important
// because the Service-level cleanup may run alongside an
// explicit shutdown.
func TestChatSessionStore_StopIdempotent(t *testing.T) {
	store := NewChatSessionStore(ChatSessionStoreConfig{
		MaxTurns: 5,
		IdleTTL:  time.Minute,
	})

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); store.Stop() }()
	go func() { defer wg.Done(); store.Stop() }()

	// Wait for both Stops to return without blocking forever.
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent Stops blocked; Stop should be idempotent")
	}
}
