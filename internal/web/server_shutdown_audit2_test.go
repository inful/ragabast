package web

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// countingService records whether the chat-session GC was
// stopped, so a test can assert that the server's shutdown
// path released it.
type countingService struct {
	fakeHumaService
	gcStopped atomic.Bool
}

func (c *countingService) StopChatSessionsGC() { c.gcStopped.Store(true) }

// TestServerStartStopsBackgroundGoroutines pins audit finding
// 3: Server.Start() shut the HTTP listener down but left every
// background goroutine running.
//
// The GC goroutines (session sweep, OAuth flow sweep, chat-session
// idle-TTL) each own a ticker loop that exits only when their
// store's Stop() is called. Stop() did call them, but Start() — the
// path a real deployment takes on SIGINT — did not. The process
// exited anyway, so the leak was invisible in production; in a
// long-lived host (tests, an embedding application, a supervisor
// that restarts the server in-process) the goroutines accumulated.
//
// The fix routes both paths through the same shutdown.
//
// Note: no t.Parallel(). NewServer writes the package-level
// globalIngestQueue, so concurrent construction races — every
// other test in this package follows the same convention.
func TestServerStartStopsBackgroundGoroutines(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Address = "127.0.0.1:0" // ephemeral port; never dialed
	svc := &countingService{}
	s := NewServer(cfg, svc)

	// Start() blocks until a signal arrives, so drive it from a
	// goroutine and give it a context it can be cancelled
	// through. The shutdown path is what we are testing.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.startAndWait(ctx) }()

	// Give the listener a moment to come up, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("startAndWait did not return after the context was cancelled")
	}

	assert.True(t, svc.gcStopped.Load(),
		"Server.Start must stop the chat-session GC goroutine on shutdown")
}

// TestServerStartAndStopAreEquivalent pins that the signal path
// and the explicit Stop path release the same resources. Before
// the fix they diverged: Stop() stopped the GC stores and
// Start() did not.
func TestServerStartAndStopAreEquivalent(t *testing.T) {
	cfg := config.DefaultConfig()

	svc := &countingService{}
	s := NewServer(cfg, svc)
	require.NoError(t, s.Stop(context.Background()))
	assert.True(t, svc.gcStopped.Load(),
		"Stop must stop the chat-session GC goroutine")

	// Stop is idempotent — calling it twice must not panic or
	// block on an already-closed channel.
	require.NoError(t, s.Stop(context.Background()))
	assert.True(t, svc.gcStopped.Load())
}
