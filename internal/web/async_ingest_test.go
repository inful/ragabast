package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/web/jobs"
	"github.com/stretchr/testify/require"
)

// TestIngestAsync_202AndStatusEventualSuccess pins the
// happy path: POST /api/ingest/async returns 202 Accepted
// with a job_id immediately; GET /api/ingest/jobs/{id}
// reports pending → completed as the worker pool drains.
func TestIngestAsync_202AndStatusEventualSuccess(t *testing.T) {
	api, svc, q := asyncIngestFixture(t)
	defer q.Stop()

	resp := api.Post("/api/ingest/async", map[string]any{
		"content": "---\nuid: doc-async-1\n---\n\n# Title\nHello",
	})

	require.Equal(t, http.StatusAccepted, resp.Code,
		"async ingest must return 202 Accepted")
	var submitResp ingestAsyncResponseBody
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &submitResp))
	require.NotEmpty(t, submitResp.JobID, "submit response must carry a job_id")
	require.Equal(t, "pending", submitResp.Status)
	require.Equal(t, "/api/ingest/jobs/"+submitResp.JobID, submitResp.StatusURL)

	// Worker pool drains the job. Poll until done or fail.
	require.Eventually(t, func() bool {
		statusResp := api.Get(submitResp.StatusURL)
		if statusResp.Code != http.StatusOK {
			return false
		}
		var sb ingestJobResponseBody
		if err := json.Unmarshal(statusResp.Body.Bytes(), &sb); err != nil {
			return false
		}
		return sb.Status == "completed"
	}, 2*time.Second, 10*time.Millisecond,
		"async ingest job must reach StatusCompleted")

	require.Equal(t, 1, svc.callCount(), "worker must invoke IngestDocument exactly once")
}

// TestIngestAsync_TooLargeRejected pins that the per-document
// size cap is enforced on the async path too. Without this,
// the async queue can be filled with multi-MiB jobs that
// pin the chunker and embedding model.
func TestIngestAsync_TooLargeRejected(t *testing.T) {
	_, _, q := asyncIngestFixture(t)
	defer q.Stop()

	// Replace the fixture's queue with one that has a tight
	// 100-byte cap so the test stays fast. The replacement
	// has its own fresh humatest.TestAPI — but the body
	// shape is the same; this test only needs the queue to
	// exercise Submit directly.
	q2 := replaceQueueMaxBytes(t, q, 100)
	defer q2.Stop()

	_, err := q2.Submit(strings.Repeat("a", 200), "192.0.2.1:1234")
	require.ErrorIs(t, err, jobs.ErrJobContentTooLarge,
		"oversized document must be rejected by the queue")
}

// TestIngestAsync_404OnUnknownJob pins that GET on an unknown
// job ID returns 404 (not 500 or 200-with-empty-body).
func TestIngestAsync_404OnUnknownJob(t *testing.T) {
	api, _, q := asyncIngestFixture(t)
	defer q.Stop()

	resp := api.Get("/api/ingest/jobs/no-such-job")
	require.Equal(t, http.StatusNotFound, resp.Code)
}

// TestIngestAsync_503WhenQueueDisabled pins the fail-closed
// behavior: when the operator has not configured an async
// queue (server.async_ingest_queue_dir empty), the endpoints
// still exist for API surface stability but return 503.
func TestIngestAsync_503WhenQueueDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	// AsyncIngestQueueDir intentionally empty.
	s := NewServer(cfg, &fakeHumaService{})

	resp := httptest.NewRecorder()
	body := `{"content":"---\nuid: doc\n---\n\n# T\nH"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest/async", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusServiceUnavailable, resp.Code,
		"async ingest disabled when queue is not configured (got %d)", resp.Code)
}

// asyncIngestFixture returns a configured async-ingest-ready
// humatest.API plus a fakeHumaService and the underlying
// queue, so each test can submit jobs and observe the state
// transitions. The queue is started with a small worker
// count and 1 MiB size cap so tests stay fast.
//
// The queue is returned directly so tests can Stop() it
// cleanly when done.
func asyncIngestFixture(t *testing.T) (humatest.TestAPI, *fakeHumaService, *jobs.Queue) {
	t.Helper()

	dir := t.TempDir()
	q, err := jobs.New(dir, 1<<20, 2)
	require.NoError(t, err)

	svc := &fakeHumaService{
		answer: "doc-async",
	}
	q.Start(jobsServiceAdapter{svc: svc}, nil)

	_, api := humatest.New(t, huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info:    &huma.Info{Title: "test", Version: "1.0.0"},
		},
		Formats: map[string]huma.Format{
			"application/json": huma.DefaultJSONFormat,
			"json":             huma.DefaultJSONFormat,
		},
		DefaultFormat: "application/json",
	})
	registerIngestJobsOperations(api, q)

	return api, svc, q
}

// replaceQueueMaxBytes returns a fresh queue with the
// requested MaxBytes, swapping out the fixture's queue.
// Used by the size-cap test to avoid having to ship a
// multi-MiB body through the JSON marshaller.
func replaceQueueMaxBytes(t *testing.T, old *jobs.Queue, maxBytes int) *jobs.Queue {
	t.Helper()
	old.Stop()

	dir := t.TempDir()
	q, err := jobs.New(dir, maxBytes, 2)
	require.NoError(t, err)

	svc := &fakeHumaService{
		answer: "doc-async",
	}
	q.Start(jobsServiceAdapter{svc: svc}, nil)
	return q
}
