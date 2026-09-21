package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQueue_DepthCountsJobsByStatus covers the headline contract
// from issue #10: the queue exposes cheap counters that /api/health
// consumes. Depth walks the directory once and tallies by status.
func TestQueue_DepthCountsJobsByStatus(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 2)
	require.NoError(t, err)

	// Empty queue: all zero.
	got := q.Depth()
	assert.Equal(t, QueueStats{}, got,
		"empty queue must report all-zero stats")

	// Mixed states — 2 pending, 1 processing, 3 completed,
	// 1 failed. Depth must count by status.
	writeJobAtMtimeForStats(t, q, "p1", StatusPending, time.Now())
	writeJobAtMtimeForStats(t, q, "p2", StatusPending, time.Now())
	writeJobAtMtimeForStats(t, q, "proc1", StatusProcessing, time.Now())
	writeJobAtMtimeForStats(t, q, "c1", StatusCompleted, time.Now().Add(-1*time.Hour))
	writeJobAtMtimeForStats(t, q, "c2", StatusCompleted, time.Now().Add(-1*time.Hour))
	writeJobAtMtimeForStats(t, q, "c3", StatusCompleted, time.Now().Add(-1*time.Hour))
	writeJobAtMtimeForStats(t, q, "f1", StatusFailed, time.Now().Add(-1*time.Hour))

	got = q.Depth()
	assert.Equal(t, 2, got.Pending, "2 pending jobs expected")
	assert.Equal(t, 1, got.Processing, "1 processing job expected")
	assert.Equal(t, 3, got.Completed, "3 completed jobs expected")
	assert.Equal(t, 1, got.Failed, "1 failed job expected")
}

// TestQueue_DepthIgnoresProcessingTmpFiles pins the safety
// guarantee: .processing.json and .tmp files are part of the
// active-write / crash-recovery state and must NEVER be
// counted as a separate state. Depth must skip them — they're
// either in-flight (and the operator should look at the
// worker count, not the file count) or transient.
func TestQueue_DepthIgnoresProcessingTmpFiles(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 2)
	require.NoError(t, err)

	writeJobAtMtimeForStats(t, q, "real-pending", StatusPending, time.Now())
	// A .processing.json file: this is in-flight from a worker.
	require.NoError(t, q.writeAtPath(&Job{
		ID:     "in-flight",
		Status: StatusProcessing,
	}, q.processingPath("in-flight")))
	// A .tmp file: this is a partial write from a crashed writer.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "abc.tmp"),
		[]byte(`{"partial":`), 0o600))

	got := q.Depth()
	assert.Equal(t, 1, got.Pending,
		"only the canonical pending file must be counted")
	assert.Equal(t, 0, got.Processing,
		".processing.json files are in-flight, not counted as a separate state")
}

// TestQueue_OldestPendingAgeReturnsTheOldestPending covers the
// "oldest_pending_age_seconds" field of the /api/health
// payload. The age is the wall-clock delta between now and
// the oldest pending file's mtime.
func TestQueue_OldestPendingAgeReturnsTheOldestPending(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 2)
	require.NoError(t, err)

	// No pending jobs: oldest age is nil.
	assert.Nil(t, q.OldestPendingAge(),
		"empty queue must report nil oldest age")

	// Two pending jobs, the older one 90s ago. Oldest age
	// must reflect the 90s one, not the 10s one.
	now := time.Now()
	writeJobAtMtimeForStats(t, q, "old", StatusPending, now.Add(-90*time.Second))
	writeJobAtMtimeForStats(t, q, "new", StatusPending, now.Add(-10*time.Second))

	got := q.OldestPendingAge()
	require.NotNil(t, got, "must report a non-nil age when pending jobs exist")
	assert.GreaterOrEqual(t, *got, 89*time.Second,
		"oldest pending age must be ≥ 89s (90s minus scheduling jitter)")
	assert.LessOrEqual(t, *got, 100*time.Second,
		"oldest pending age must be ≤ 100s (90s plus scheduling jitter)")

	// Now mark old as completed; only "new" remains pending.
	// Oldest age should drop to ~10s.
	old, _ := q.Get("old")
	now2 := time.Now()
	old.Status = StatusCompleted
	old.CompletedAt = &now2
	require.NoError(t, q.writeAtPath(old, q.path("old")))

	got = q.OldestPendingAge()
	require.NotNil(t, got)
	assert.LessOrEqual(t, *got, 30*time.Second,
		"after marking 'old' complete, oldest pending age should be near 10s")
}

// TestQueue_DepthSkipsEmptyDir is the boundary case: a queue
// whose persistence_dir exists but was just created must
// return all-zero stats rather than erroring out. /api/health
// relies on this — the first health check after a fresh
// install must succeed.
func TestQueue_DepthSkipsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 2)
	require.NoError(t, err)

	// No jobs added; dir exists.
	got := q.Depth()
	assert.Equal(t, QueueStats{}, got)
}

// writeJobAtMtimeForStats is a small helper that writes a Job
// at the canonical .json path with a given status and mtime.
// Uses the queue's writeAtPath so the file format matches
// what Depth reads.
func writeJobAtMtimeForStats(t *testing.T, q *Queue, id string, status Status, mtime time.Time) {
	t.Helper()
	now := time.Now().UTC()
	j := &Job{
		ID:        id,
		Status:    status,
		CreatedAt: now.Add(-2 * time.Hour),
	}
	if status == StatusCompleted || status == StatusFailed {
		ts := now.Add(-30 * time.Minute)
		j.StartedAt = &ts
		j.CompletedAt = &ts
	}
	if status == StatusFailed {
		j.Error = "synthetic"
	}
	require.NoError(t, q.writeAtPath(j, q.path(id)))
	path := q.path(id)
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}
