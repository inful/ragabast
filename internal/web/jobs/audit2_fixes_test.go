package jobs

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQueue_CompletedJobFileDropsContent pins audit finding
// 4: once a job finishes, the on-disk record must not keep
// the full document body. Nothing reads Content after
// completion, so keeping it multiplies disk use by the
// retention window (default 7 days for completed, 30 for
// failed).
//
// This is also the disk-growth half of finding 1: List()
// reads and JSON-parses every job file in the directory, so
// a 200 KB body per finished job makes
// GET /api/ingest/jobs parse megabytes to return 50 rows.
func TestQueue_CompletedJobFileDropsContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)
	defer q.Stop()

	bigBody := make([]byte, 200_000)
	for i := range bigBody {
		bigBody[i] = 'x'
	}
	j, err := q.Submit(string(bigBody), "192.0.2.1:1234", "req-big")
	require.NoError(t, err)

	before, err := os.Stat(q.path(j.ID))
	require.NoError(t, err)

	q.processOne(j.ID, t.Context(), &fakeService{}, nil)

	after, err := os.Stat(q.path(j.ID))
	require.NoError(t, err)

	// The finished file must be dramatically smaller than
	// the submitted one. A 200 KB body dominates the JSON;
	// dropping it should shrink the file by ~99%.
	assert.Less(t, after.Size(), before.Size()/10,
		"completed job file kept the document body (%d -> %d bytes)",
		before.Size(), after.Size())

	// The status must still be readable — we are dropping
	// the body, not the record.
	onDisk, err := q.Get(j.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, onDisk.Status)
	assert.Empty(t, onDisk.Content)
}

// TestQueue_FailedJobFileDropsContent covers the same
// contract on the failure path. Failed jobs have the longer
// TTL (30 days by default), so keeping the body there is
// worse, not better.
func TestQueue_FailedJobFileDropsContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)
	defer q.Stop()

	bigBody := make([]byte, 100_000)
	for i := range bigBody {
		bigBody[i] = 'y'
	}
	j, err := q.Submit(string(bigBody), "192.0.2.1:1234", "req-big-fail")
	require.NoError(t, err)

	svc := &fakeService{failOn: map[string]error{string(bigBody): os.ErrPermission}}
	q.processOne(j.ID, t.Context(), svc, nil)

	onDisk, err := q.Get(j.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, onDisk.Status)
	assert.Empty(t, onDisk.Content,
		"failed job file must also drop the body (30-day TTL)")
	assert.NotEmpty(t, onDisk.Error,
		"the error message must survive — operators read it")
}

// TestQueue_InFlightJobFileKeepsContent guards the other
// side: the body MUST still be on disk while the job is
// being processed, because that is what crash recovery
// reads. If this ever fails, a crash mid-ingest loses the
// document and the job can never be replayed.
func TestQueue_InFlightJobFileKeepsContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)
	defer q.Stop()

	const body = "content-that-must-survive-a-crash"
	j, err := q.Submit(body, "192.0.2.1:1234", "req-recover")
	require.NoError(t, err)

	// Simulate a crash: claim the job the way a worker
	// does, then walk away without finishing.
	require.NoError(t, os.Rename(q.path(j.ID), q.processingPath(j.ID)))

	inFlight, err := q.getAtProcessingPath(j.ID)
	require.NoError(t, err)
	assert.Equal(t, body, inFlight.Content,
		"an in-flight job must keep its content for recovery")
}

// TestQueue_ClaimRaceLoserDoesNotIngest is the direct test
// for audit finding 1's post-claim re-read.
//
// The race the old code lost to:
//
//	loser reads .json  -> status pending
//	winner claims, ingests, renames .processing.json -> .json
//	loser's claim rename succeeds (the winner put .json back)
//	loser ingests the same document a second time
//
// The fix re-reads the file after the claim and aborts when the
// claimed job is already finished.
//
// The loser's pre-claim read is bypassed on purpose. The
// winner's record is left at .processing.json (the loser reads
// it there as its stale snapshot) while .json holds an
// undecodable file, so the loser reaches the claim with a
// pending job in hand — and the claim rename then picks up the
// bytes that are actually on disk. That is the same shape as
// the race above: the loser ends up holding a record that is
// not the one it read.
func TestQueue_ClaimRaceLoserDoesNotIngest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	q, err := New(dir, 1<<20, 1)
	require.NoError(t, err)
	defer q.Stop()

	seed, err := New(dir, 1<<20, 1)
	require.NoError(t, err)
	j, err := seed.Submit("race-doc", "192.0.2.1:1234", "req-race")
	require.NoError(t, err)
	seed.Stop()

	// The winner is mid-flight: it holds the job at
	// .processing.json. The loser reads its pending snapshot
	// from there.
	require.NoError(t, os.Rename(q.path(j.ID), q.processingPath(j.ID)))

	// The winner finished and published a record the loser
	// cannot decode at .json. The loser's pre-claim read of
	// .json fails, so it falls back to the pending snapshot at
	// .processing.json and proceeds to the claim — exactly the
	// point where the old code ingested without re-reading.
	require.NoError(t, os.WriteFile(q.path(j.ID), []byte(`{"status":"pen`), 0o600))

	svc := &fakeService{}
	q.processOne(j.ID, t.Context(), svc, nil)

	assert.Equal(t, 0, svc.callCount(),
		"a job whose claimed record is not readable must not be ingested")

	// The record must not be stranded at .processing.json: no
	// reader looks there, so the job would vanish.
	_, err = os.Stat(q.processingPath(j.ID))
	assert.True(t, os.IsNotExist(err),
		"the claimed file must be restored to .json, not left at .processing.json")
}

// TestQueue_TwoQueuesNeverDoubleIngest is the real
// reproduction of finding 1. Two Queue instances share one
// directory (the multi-process / multi-pod shape the
// recoverUnfinished fence is written for). Both recover the
// same pending job and both dispatch it. Exactly one may
// call IngestDocument.
//
// Run with -count to get a meaningful sample: the old
// code failed this roughly 1 run in 20.
func TestQueue_TwoQueuesNeverDoubleIngest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	seed, err := New(dir, 1<<20, 1)
	require.NoError(t, err)
	j, err := seed.Submit("shared-doc", "192.0.2.1:1234", "req-two-queues")
	require.NoError(t, err)
	seed.Stop()

	q1, err := New(dir, 1<<20, 1)
	require.NoError(t, err)
	defer q1.Stop()
	q2, err := New(dir, 1<<20, 1)
	require.NoError(t, err)
	defer q2.Stop()

	svc1 := &fakeService{}
	svc2 := &fakeService{}
	q1.Start(svc1, nil)
	q2.Start(svc2, nil)

	require.Eventually(t, func() bool {
		got, gerr := q1.Get(j.ID)
		return gerr == nil && got.Status == StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"job must reach completed")

	assert.Equal(t, 1, svc1.callCount()+svc2.callCount(),
		"exactly one worker may ingest a shared job")
}
