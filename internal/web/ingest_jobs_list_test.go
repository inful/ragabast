package web

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragabast/internal/web/jobs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIngestJobsList_AllByDefault pins the headline behavior of
// issue #44: GET /api/ingest/jobs returns every job when no
// status filter is supplied. The total field tells the operator
// how many jobs exist; the jobs array is the result page.
func TestIngestJobsList_AllByDefault(t *testing.T) {
	api, _, q := asyncIngestFixture(t)
	defer q.Stop()

	// Three pending jobs (haven't been processed yet — q.Stop
	// at the end of the test prevents worker pool draining).
	writeJobAtStatus(t, q, "j-pending-1", jobs.StatusPending, time.Now())
	writeJobAtStatus(t, q, "j-pending-2", jobs.StatusPending, time.Now().Add(-time.Hour))
	writeJobAtStatus(t, q, "j-failed-1", jobs.StatusFailed, time.Now().Add(-30*time.Minute))

	for _, p := range []string{
		"/api/ingest/jobs",
		"/api/ingest/jobs/",
		"/api/ingest/async",
	} {
		resp := api.Get(p)
		t.Logf("GET %s → %d %s", p, resp.Code, strings.SplitN(resp.Body.String(), "\n", 2)[0])
	}

	resp := api.Get("/api/ingest/jobs")
	require.Equal(t, http.StatusOK, resp.Code, "got body: %s", resp.Body.String())

	var body ingestJobsListResponseBody
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, 3, body.Total, "total must reflect every job on disk")
	assert.Len(t, body.Jobs, 3, "jobs array must contain every job")
}

// TestIngestJobsList_FilterByStatus pins the status-filter
// half of #44's acceptance: ?status=failed returns only the
// failed jobs. The other statuses are filtered out by the
// queue's List(statusFilter) helper.
func TestIngestJobsList_FilterByStatus(t *testing.T) {
	api, _, q := asyncIngestFixture(t)
	defer q.Stop()

	writeJobAtStatus(t, q, "j-pending-1", jobs.StatusPending, time.Now())
	writeJobAtStatus(t, q, "j-failed-1", jobs.StatusFailed, time.Now().Add(-time.Hour))
	writeJobAtStatus(t, q, "j-failed-2", jobs.StatusFailed, time.Now().Add(-30*time.Minute))

	resp := api.Get("/api/ingest/jobs?status=failed")

	require.Equal(t, http.StatusOK, resp.Code)

	var body ingestJobsListResponseBody
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, 2, body.Total,
		"total must reflect only failed jobs (status filter applied)")
	assert.Len(t, body.Jobs, 2)
	for _, j := range body.Jobs {
		assert.Equal(t, "failed", j.Status,
			"every job in the response must have status=failed")
	}
}

// TestIngestJobsList_Pagination pins the limit/offset story:
// callers can page through large job sets without loading the
// whole list into memory.
func TestIngestJobsList_Pagination(t *testing.T) {
	api, _, q := asyncIngestFixture(t)
	defer q.Stop()

	// Seed 5 jobs with deterministic IDs.
	for i, id := range []string{"j-a", "j-b", "j-c", "j-d", "j-e"} {
		writeJobAtStatus(t, q, id, jobs.StatusPending, time.Now().Add(-time.Duration(i)*time.Second))
	}

	// limit=2&offset=1 → page 2 (skip j-a, return j-b and j-c).
	resp := api.Get("/api/ingest/jobs?limit=2&offset=1")

	require.Equal(t, http.StatusOK, resp.Code)

	var body ingestJobsListResponseBody
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, 5, body.Total, "total is the full count, not the page size")
	assert.Len(t, body.Jobs, 2, "page size must equal limit")
}

// TestIngestJobsList_EmptyList covers the edge case: no jobs on
// disk returns 200 with an empty jobs array, not 404. Operators
// monitor this endpoint on a schedule and 404 would look like
// a service outage.
func TestIngestJobsList_EmptyList(t *testing.T) {
	api, _, q := asyncIngestFixture(t)
	defer q.Stop()

	resp := api.Get("/api/ingest/jobs")

	require.Equal(t, http.StatusOK, resp.Code)

	var body ingestJobsListResponseBody
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, 0, body.Total)
	assert.Empty(t, body.Jobs)
}

// TestIngestJobsList_DefaultLimitApplied covers the "no params"
// case: the endpoint applies sensible defaults (limit=50,
// offset=0) when callers don't pass them.
func TestIngestJobsList_DefaultLimitApplied(t *testing.T) {
	api, _, q := asyncIngestFixture(t)
	defer q.Stop()

	// Seed 60 jobs — more than the default page size.
	for i := range 60 {
		writeJobAtStatus(t, q, "j-bulk-"+itoa(i), jobs.StatusPending,
			time.Now().Add(-time.Duration(i)*time.Second))
	}

	resp := api.Get("/api/ingest/jobs")

	require.Equal(t, http.StatusOK, resp.Code)

	var body ingestJobsListResponseBody
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))

	assert.Equal(t, 60, body.Total, "total must reflect all jobs even with default page size")
	assert.Len(t, body.Jobs, 50, "default page size is 50")
}

// --- helpers ---

// writeJobAtStatus writes a synthetic Job file at the queue's
// persistence dir at status <s>, with mtime <mtime>. Used by the
// list tests because the worker pool drains jobs too quickly
// for the response to be deterministic.
//
// Writes the file directly to disk (the queue's writeAtPath /
// path helpers are unexported; cross-package test access would
// require a public helper or a test-only file in the jobs
// package). The JSON shape matches what jobs.Queue writes so
// getFromPath round-trips on the read side.
func writeJobAtStatus(t *testing.T, q *jobs.Queue, id string, s jobs.Status, mtime time.Time) {
	t.Helper()
	j := &jobs.Job{
		ID:        id,
		Status:    s,
		CreatedAt: mtime,
	}
	if s == jobs.StatusCompleted || s == jobs.StatusFailed {
		ts := mtime.Add(time.Minute)
		j.StartedAt = &ts
		j.CompletedAt = &ts
	}
	if s == jobs.StatusFailed {
		j.Error = "synthetic"
	}
	data, err := json.Marshal(j)
	require.NoError(t, err)
	// q.path is unexported; reconstruct via Dir() + known
	// naming convention (id + ".json"). The convention is
	// already used by jobs.Queue.path.
	path := q.Dir() + "/" + id + ".json"
	require.NoError(t, os.WriteFile(path, data, 0o600))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

// itoa is a tiny helper to avoid pulling strconv into the test.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}
