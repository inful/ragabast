package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeService is a deterministic IngestDocument
// implementation the queue tests use instead of the real
// service. It records every call so the tests can assert
// that persistence + recovery actually re-runs the work.
type fakeService struct {
	mu     sync.Mutex
	calls  []string // job contents, in invocation order
	delay  time.Duration
	failOn map[string]error
}

func (f *fakeService) IngestDocument(_ context.Context, content string) (string, int, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failOn[content]; ok {
		return "", 0, err
	}
	f.calls = append(f.calls, content)
	return "doc-" + content, 3, nil
}

func (f *fakeService) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestQueue_SubmitAndProcess is the smoke test: submit a job,
// worker processes it, status moves to completed.
func TestQueue_SubmitAndProcess(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 2)
	require.NoError(t, err)

	svc := &fakeService{}
	q.Start(svc, nil)
	defer q.Stop()

	j, err := q.Submit("hello", "192.0.2.1:1234")
	require.NoError(t, err)
	require.NotEmpty(t, j.ID)
	require.Equal(t, StatusPending, j.Status)

	// Poll for completion (async — no blocking wait API).
	require.Eventually(t, func() bool {
		got, gerr := q.Get(j.ID)
		return gerr == nil && got.Status == StatusCompleted
	}, 2*time.Second, 10*time.Millisecond,
		"job must transition to completed")

	got, err := q.Get(j.ID)
	require.NoError(t, err)
	require.Equal(t, "doc-hello", got.DocumentID)
	require.Equal(t, 3, got.Chunks)
	require.NotNil(t, got.StartedAt)
	require.NotNil(t, got.CompletedAt)

	require.Equal(t, 1, svc.callCount(),
		"worker must invoke IngestDocument exactly once")
}

// TestQueue_PersistsAcrossRestart is the headline
// persistence test: simulate a process crash mid-ingest
// (worker claimed the job, renamed pending → processing,
// then died), restart the queue against the same dir,
// verify the unfinished job is re-enqueued and processed.
//
// We simulate the crash by manually moving the job file
// from .json to .processing.json with status=processing —
// the exact on-disk shape the worker would have left behind
// had it died after the atomic claim. This avoids a race
// against a real worker's completion timing that made the
// earlier implementation flaky under -race.
func TestQueue_PersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	// First "process" — create the queue, submit one job,
	// then simulate the crash BEFORE starting workers.
	// Without workers running, the job file sits at
	// <id>.json with status=pending. We then move it to
	// <id>.processing.json with status=processing — the
	// state a worker would leave behind if it died after
	// claiming the job but before completing it.
	q1, err := New(dir, 1<<20, 2)
	require.NoError(t, err)

	j, err := q1.Submit("durable-content", "192.0.2.1:1234")
	require.NoError(t, err)

	// Read the pending file, rewrite with status=processing
	// to simulate "worker died mid-flight", then move it to
	// the processing path. recoverUnfinished scans both
	// paths and re-enqueues whichever jobs are not
	// completed/failed.
	srcPath := q1.path(j.ID)
	dstPath := q1.processingPath(j.ID)
	pending, gerr := q1.Get(j.ID)
	require.NoError(t, gerr)
	pending.Status = StatusProcessing
	now := time.Now().UTC()
	pending.StartedAt = &now
	require.NoError(t, os.Rename(srcPath, dstPath))
	require.NoError(t, q1.writeAtPath(pending, dstPath))

	// Second "process" — fresh Queue, same directory. Recovery
	// must pick up the processing file, reset it to pending,
	// and feed it to a worker.
	svc2 := &fakeService{}
	q2, err := New(dir, 1<<20, 2)
	require.NoError(t, err)
	q2.Start(svc2, nil)
	defer q2.Stop()

	// The unfinished job must be picked up and processed.
	require.Eventually(t, func() bool {
		got, gerr := q2.Get(j.ID)
		return gerr == nil && got.Status == StatusCompleted
	}, 2*time.Second, 10*time.Millisecond,
		"unfinished job must be re-processed after restart")

	require.Equal(t, 1, svc2.callCount(),
		"recovered job must run exactly once on the new process")
}

// TestQueue_TooLargeRejected pins the per-job content cap.
// The same cap the synchronous ingest handler enforces must
// be enforced on async submits too, otherwise an attacker
// can DoS the queue by filling it with multi-MiB jobs.
func TestQueue_TooLargeRejected(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 100, 1)
	require.NoError(t, err)
	q.Start(&fakeService{}, nil)
	defer q.Stop()

	_, err = q.Submit(strings.Repeat("a", 200), "192.0.2.1:1234")
	require.ErrorIs(t, err, ErrJobContentTooLarge)
}

// TestQueue_RejectsInvalidID pins that Get rejects unknown
// job IDs cleanly so the HTTP layer can return 404.
func TestQueue_RejectsInvalidID(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	_, err = q.Get("no-such-job")
	require.ErrorIs(t, err, ErrJobNotFound)
}

// TestQueue_FailedJobRecordsError pins that an ingest failure
// moves the job to StatusFailed and records the error
// message. The HTTP status endpoint surfaces this to the
// caller so docbuilder can decide to retry.
func TestQueue_FailedJobRecordsError(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	svc := &fakeService{
		failOn: map[string]error{
			"bad": errors.New("parser exploded"),
		},
	}
	q.Start(svc, nil)
	defer q.Stop()

	j, err := q.Submit("bad", "192.0.2.1:1234")
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		got, gerr := q.Get(j.ID)
		return gerr == nil && got.Status == StatusFailed
	}, 2*time.Second, 10*time.Millisecond,
		"failed job must reach StatusFailed")

	got, err := q.Get(j.ID)
	require.NoError(t, err)
	require.Contains(t, got.Error, "parser exploded")
}

// TestQueue_ConcurrentSubmits pins that many concurrent
// submitters do not race on the JSON files or the pending
// channel. Without the atomic rename / unique UUID /
// buffered channel combination, this would produce
// duplicate IDs or lost writes.
func TestQueue_ConcurrentSubmits(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 4)
	require.NoError(t, err)

	svc := &fakeService{}
	q.Start(svc, nil)
	defer q.Stop()

	const n = 50
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			j, err := q.Submit("content-"+string(rune('a'+i%26)), "192.0.2.1:1234")
			if err != nil {
				t.Errorf("submit %d: %v", i, err)
				return
			}
			ids[i] = j.ID
		}(i)
	}
	wg.Wait()

	// All IDs must be unique.
	seen := make(map[string]struct{}, n)
	for _, id := range ids {
		_, dup := seen[id]
		require.False(t, dup, "duplicate job id: %s", id)
		seen[id] = struct{}{}
	}

	// All jobs must eventually complete.
	require.Eventually(t, func() bool {
		for _, id := range ids {
			got, gerr := q.Get(id)
			if gerr != nil || got.Status != StatusCompleted {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond,
		"every concurrent submit must complete")

	// And every job's IngestDocument must have been called
	// exactly once (atomic claim prevents double-processing).
	require.Equal(t, n, svc.callCount())
}

// TestQueue_BoundedConcurrency pins that the worker pool
// honors its concurrency limit. With workers=2 and 10 jobs
// queued, at most 2 are in-flight at any moment.
func TestQueue_BoundedConcurrency(t *testing.T) {
	dir := t.TempDir()
	const workers = 2
	q, err := New(dir, 1<<20, workers)
	require.NoError(t, err)

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	svc := &boundedService{
		delay: 50 * time.Millisecond,
		onStart: func() {
			cur := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
		},
	}
	q.Start(svc, nil)
	defer q.Stop()

	const n = 10
	for range n {
		_, err := q.Submit("job", "")
		require.NoError(t, err)
	}

	// Wait for everything to drain.
	require.Eventually(t, func() bool {
		jobs, _ := q.List("")
		for _, j := range jobs {
			if j.Status != StatusCompleted && j.Status != StatusFailed {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond)

	require.LessOrEqual(t, maxInFlight.Load(), int32(workers),
		"max concurrent jobs must not exceed worker count")
}

// boundedService is a Service implementation that calls onStart
// when an ingest begins; the test uses it to observe
// concurrency.
type boundedService struct {
	delay   time.Duration
	onStart func()
}

func (b *boundedService) IngestDocument(_ context.Context, _ string) (string, int, error) {
	if b.onStart != nil {
		b.onStart()
	}
	if b.delay > 0 {
		time.Sleep(b.delay)
	}
	return "doc", 1, nil
}

// TestQueue_AtomicClaimNoDoubleProcess pins that even if
// two workers somehow end up holding the same job ID, the
// atomic rename pending → processing prevents the work from
// being done twice. This is the safety net behind the
// worker pool's channel coordination.
//
// The "somehow" path here is: we Submit one job, then
// manually re-enqueue the same ID twice in quick succession.
// Without atomic claim, the second worker would also pick
// it up.
func TestQueue_AtomicClaimNoDoubleProcess(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 4)
	require.NoError(t, err)

	svc := &fakeService{}
	q.Start(svc, nil)
	defer q.Stop()

	j, err := q.Submit("once", "")
	require.NoError(t, err)

	// Force-enqueue the same ID twice more. The atomic
	// rename inside processOne means only one of these
	// three enqueues will actually do work; the others will
	// find the file already moved to .processing.json (or
	// already completed) and skip.
	q.pending <- j.ID
	q.pending <- j.ID

	require.Eventually(t, func() bool {
		got, gerr := q.Get(j.ID)
		return gerr == nil && got.Status == StatusCompleted
	}, 2*time.Second, 10*time.Millisecond)

	require.Equal(t, 1, svc.callCount(),
		"triple-enqueued job must invoke IngestDocument exactly once")
}

// _ keeps the filepath import live for future tests that
// might want to inspect the on-disk layout.
var _ = filepath.Join

// strings is needed for the TooLargeRejected test.
var _ = strings.Repeat

// strings is in the test file via the package import below;
// declared as a package-level blank assignment so the
// imports list stays explicit. (Avoids the "imported and not
// used" linter when we trim a test case later.)
