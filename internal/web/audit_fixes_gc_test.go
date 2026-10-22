package web

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionStore_StopIdempotentAndExitsFast pins that the
// sweepLoop goroutine spawned in newSessionStore exits when
// the test calls Stop. A regression that fails to close s.stop
// would leave the goroutine running and the test would hang
// on t.Cleanup. Audit finding 2 (audit "sessionStore.Sweep()
// exists but no caller runs it" — this commit wires Stop and
// makes the loop on its own).
func TestSessionStore_StopIdempotentAndExitsFast(t *testing.T) {
	store := newSessionStore(time.Hour)

	done := make(chan struct{})
	go func() { store.Stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return within 1s — sweepLoop likely didn't exit on close(s.stop)")
	}

	// Second call must not panic / block.
	store.Stop()
}

// TestFlowStateStore_StopAndGc pins that the new ticker in
// newFlowStateStore stops cleanly and that gc() removes
// entries older than oauthFlowStateTTL (10m default).
// Audit finding 13.
func TestFlowStateStore_StopAndGc(t *testing.T) {
	store := newFlowStateStore()
	t.Cleanup(store.Stop)

	// Forge a flow whose CreatedAt is older than
	// oauthFlowStateTTL so gc() removes it.
	store.Put("stale-state", &flowState{
		providerName: "github",
		verifier:     "verifier",
		next:         "/",
		createdAt:    time.Now().Add(-2 * oauthFlowStateTTL),
	})

	// A fresh flow whose CreatedAt is "now" survives gc().
	store.Put("fresh-state", &flowState{
		providerName: "github",
		verifier:     "verifier",
		next:         "/",
		createdAt:    time.Now(),
	})

	store.gc()

	assert.Nil(t, store.Pop("stale-state"), "stale flow must be evicted by gc()")
	got := store.Pop("fresh-state")
	assert.NotNil(t, got, "fresh flow must survive gc()")

	// Stop should exit cleanly even when the goroutine is
	// mid-tick.
	store.Stop()
}

// TestFlowStateStore_StopIdempotent: a regression that
// closes s.stop twice would panic on send-to-closed-channel.
func TestFlowStateStore_StopIdempotent(t *testing.T) {
	store := newFlowStateStore()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); store.Stop() }()
	go func() { defer wg.Done(); store.Stop() }()

	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent Stops blocked; Stop should be idempotent")
	}
	require.NotPanics(t, func() { store.Stop() }, "third Stop must not panic")
}
