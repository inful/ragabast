package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestQueue_RunCleanup_RemovesOldCompletedJobs covers the core
// happy path: a completed job older than completedTTL is
// removed; a completed job newer than completedTTL is kept.
// The cleanup walks the directory once and reports how many
// files it removed.
func TestQueue_RunCleanup_RemovesOldCompletedJobs(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	// Seed one old completed job and one fresh completed job.
	// The JSON payload must round-trip through the same
	// getFromPath code path the queue uses.
	old := writeJobAtMtime(t, q, "old-completed", StatusCompleted, time.Now().Add(-1*time.Hour))
	fresh := writeJobAtMtime(t, q, "fresh-completed", StatusCompleted, time.Now().Add(-1*time.Minute))

	removed, err := q.RunCleanup(30*time.Minute, 0)
	require.NoError(t, err)
	require.Equal(t, 1, removed,
		"exactly one stale completed job should be removed")

	// Old is gone; fresh remains.
	_, gerr := q.Get(old.ID)
	require.ErrorIs(t, gerr, ErrJobNotFound, "old completed job must be removed")
	got, gerr := q.Get(fresh.ID)
	require.NoError(t, gerr, "fresh completed job must NOT be removed")
	require.Equal(t, StatusCompleted, got.Status)
}

// TestQueue_RunCleanup_KeepsFailedLongerThanCompleted pins that
// failed jobs use the longer TTL. A job that failed an hour ago
// stays when the completed TTL is 30 minutes (so it would have
// been swept if it were a success), but the failed TTL is 2
// hours — the failed job stays.
func TestQueue_RunCleanup_KeepsFailedLongerThanCompleted(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	// 1 hour ago, failed. completedTTL=30m would sweep a
	// success at this age; failedTTL=2h keeps it.
	failed := writeJobAtMtime(t, q, "recently-failed", StatusFailed, time.Now().Add(-1*time.Hour))

	removed, err := q.RunCleanup(30*time.Minute, 2*time.Hour)
	require.NoError(t, err)
	require.Zero(t, removed,
		"the failed job is within its TTL — nothing should be removed")

	_, gerr := q.Get(failed.ID)
	require.NoError(t, gerr,
		"failed job within failedTTL must remain on disk")
}

// TestQueue_RunCleanup_RemovesOldFailedJobs covers the
// symmetric case: a failed job past the failed TTL is swept.
func TestQueue_RunCleanup_RemovesOldFailedJobs(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	oldFailed := writeJobAtMtime(t, q, "ancient-failed", StatusFailed, time.Now().Add(-72*time.Hour))
	freshFailed := writeJobAtMtime(t, q, "recent-failed", StatusFailed, time.Now().Add(-1*time.Minute))

	removed, err := q.RunCleanup(30*time.Minute, 24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	_, gerr := q.Get(oldFailed.ID)
	require.ErrorIs(t, gerr, ErrJobNotFound, "ancient failed job must be swept")
	_, gerr = q.Get(freshFailed.ID)
	require.NoError(t, gerr, "recent failed job must survive")
}

// TestQueue_RunCleanup_SkipsProcessingFiles pins the safety
// guarantee: a worker holding a job leaves the file at
// `.processing.json` regardless of age, and cleanup must NOT
// touch it. Otherwise an in-flight ingest would have its
// file yanked mid-process.
func TestQueue_RunCleanup_SkipsProcessingFiles(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	// Seed a .processing.json file as if a worker is mid-ingest.
	processing := &Job{
		ID:        "in-flight",
		Content:   "x",
		Status:    StatusProcessing,
		CreatedAt: time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, writeJobProcessing(q, processing))

	// Even with TTL=1ns, the file must survive because
	// it's a .processing.json file, not a finished job.
	removed, err := q.RunCleanup(1*time.Nanosecond, 1*time.Nanosecond)
	require.NoError(t, err)
	require.Zero(t, removed,
		".processing.json files must never be swept")

	// The file must still exist at its processing path —
	// q.Get looks at the canonical .json path, which the
	// in-flight job has not reached yet, so we stat directly.
	if _, statErr := os.Stat(q.processingPath(processing.ID)); statErr != nil {
		t.Errorf("in-flight job must remain on disk at %s: %v",
			q.processingPath(processing.ID), statErr)
	}
}

// TestQueue_RunCleanup_ZeroTTLDisablesCleanup pins the
// operator override: setting either TTL to zero means "never
// clean up". Operators can disable the feature entirely with
// a single config change.
func TestQueue_RunCleanup_ZeroTTLDisablesCleanup(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	// Job from 10 years ago. Without TTL=0, this would be
	// swept immediately.
	ancient := writeJobAtMtime(t, q, "ancient-completed", StatusCompleted, time.Now().Add(-10*365*24*time.Hour))

	removed, err := q.RunCleanup(0, 0)
	require.NoError(t, err)
	require.Zero(t, removed,
		"TTL=0 must disable cleanup entirely")

	_, gerr := q.Get(ancient.ID)
	require.NoError(t, gerr,
		"TTL=0 must preserve every job regardless of age")
}

// TestQueue_RunCleanup_LogsViaAuditFunc pins that the cleanup
// pass surfaces a single audit event per run with the count
// of removed files. Operators rely on this for monitoring.
func TestQueue_RunCleanup_LogsViaAuditFunc(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	writeJobAtMtime(t, q, "old-1", StatusCompleted, time.Now().Add(-1*time.Hour))
	writeJobAtMtime(t, q, "old-2", StatusCompleted, time.Now().Add(-2*time.Hour))
	writeJobAtMtime(t, q, "old-3", StatusFailed, time.Now().Add(-48*time.Hour))

	var events []map[string]any
	var mu sync.Mutex
	audit := func(event string, fields ...any) {
		mu.Lock()
		defer mu.Unlock()
		entry := map[string]any{"event": event}
		for i := 0; i+1 < len(fields); i += 2 {
			k, _ := fields[i].(string)
			entry[k] = fields[i+1]
		}
		events = append(events, entry)
	}

	_, err = q.RunCleanupWithAudit(30*time.Minute, 24*time.Hour, audit)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	// Find the cleanup summary event.
	var summary map[string]any
	for _, e := range events {
		if e["event"] == "jobs.cleanup_completed" {
			summary = e
			break
		}
	}
	require.NotNil(t, summary, "cleanup pass must emit a summary audit event")
	require.Equal(t, 3, summary["removed"],
		"summary must report total removed (2 completed + 1 failed)")
}

// TestQueue_RunCleanup_ManyStaleJobs scales up: 1000 stale
// jobs must all be removed in a single pass. This pins both
// correctness and the basic performance ceiling — if the
// cleanup walks the directory one file at a time with
// expensive syscalls, this test will become a performance
// regression detector.
func TestQueue_RunCleanup_ManyStaleJobs(t *testing.T) {
	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)

	const n = 1000
	old := time.Now().Add(-1 * time.Hour)
	for i := range n {
		// Cycle through completed/failed to exercise both paths.
		status := StatusCompleted
		if i%3 == 0 {
			status = StatusFailed
		}
		writeJobAtMtime(t, q, jobIDForIndex(i), status, old)
	}

	// Failed TTL of 1h catches every seeded job.
	start := time.Now()
	removed, err := q.RunCleanup(30*time.Minute, 1*time.Hour)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Equal(t, n, removed,
		"every seeded stale job must be removed")
	t.Logf("cleaned %d stale jobs in %s", removed, elapsed)

	// Directory must be empty (or contain only the
	// processing files we never seeded).
	entries, lerr := os.ReadDir(dir)
	require.NoError(t, lerr)
	for _, e := range entries {
		name := e.Name()
		require.False(t,
			strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".processing.json"),
			"no .json files should remain after cleanup; found %s", name)
	}
}

// writeJobAtMtime writes a Job JSON file to the queue's
// directory with the given mtime. Uses the queue's writeAtPath
// for the .json write so the file is valid for getFromPath.
func writeJobAtMtime(t *testing.T, q *Queue, id string, status Status, mtime time.Time) *Job {
	t.Helper()
	now := time.Now().UTC()
	j := &Job{
		ID:        id,
		Content:   "test",
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
	return j
}

// writeJobProcessing writes a Job to .processing.json at the
// current time (not backdated) — simulates a worker that just
// claimed the file. Used to verify cleanup never touches it
// even when the file is old.
func writeJobProcessing(q *Queue, j *Job) error {
	return q.writeAtPath(j, q.processingPath(j.ID))
}

// jobIDForIndex returns a stable job ID for a given test
// index. Padding ensures lexical ordering doesn't conflict
// with sort order assertions.
func jobIDForIndex(i int) string {
	return "job-" + filepath.Base(filepath.Join("0", strings.Repeat("0", 6-len(intToStr(i)))+intToStr(i)))
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	// Tiny helper; avoids strconv import in the test file.
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
