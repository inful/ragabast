package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQueue_StopAbortsInFlightWorker is the regression test
// for the second half of issue #31: Queue.Stop signals
// in-flight workers to abort, not just wait for them to
// finish naturally. A worker that's mid-IngestDocument must
// see the stop signal and return promptly.
//
// Without the fix, the worker runs processOne to completion
// (the slow IngestDocument holds for the full delay), then
// the select picks up the next loop iteration and reads the
// stopped channel. Stop blocks for the entire job duration.
func TestQueue_StopAbortsInFlightWorker(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 2)
	require.NoError(t, err)

	// Track whether the service's IngestDocument saw the
	// cancellation. With the fix, the worker observes ctx
	// cancellation and bails; the service's select sees
	// ctx.Done() and returns context.Canceled.
	var sawCancel atomic.Bool
	svc := &slowServiceCancel{
		delay:  5 * time.Second,
		onDone: func(canceled bool) { sawCancel.Store(canceled) },
	}

	q.Start(svc, nil)
	defer q.Stop()

	// Submit one job so the pool has something to chew on.
	j, err := q.Submit("slow", "127.0.0.1:0", "")
	require.NoError(t, err)

	// Give the worker a moment to claim and start the job.
	// The slow service sleeps 5s; the worker is mid-sleep now.
	time.Sleep(50 * time.Millisecond)

	// Stop should abort the in-flight worker promptly.
	start := time.Now()
	q.Stop()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond,
		"Stop must abort in-flight workers; elapsed=%s", elapsed)

	// The cancel signal fires synchronously inside Stop; the
	// worker observes it asynchronously. Poll briefly so the
	// assertion doesn't race the goroutine.
	assert.Eventually(t, func() bool {
		return sawCancel.Load()
	}, 1*time.Second, 5*time.Millisecond,
		"the slow service must observe the ctx cancellation after Stop")
	_ = j // suppress unused warning
}

// slowServiceCancel is a fake jobs.Service that blocks on
// either a fixed delay or a ctx cancel — whichever comes
// first. It records whether the cancel path was taken.
type slowServiceCancel struct {
	delay  time.Duration
	onDone func(canceled bool)
}

func (s *slowServiceCancel) IngestDocument(ctx context.Context, _ string) (string, int, error) {
	select {
	case <-time.After(s.delay):
		s.onDone(false)
		return "doc-slow", 1, nil
	case <-ctx.Done():
		s.onDone(true)
		return "", 0, ctx.Err()
	}
}
