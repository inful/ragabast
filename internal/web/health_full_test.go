package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/web/jobs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHealthFull_ReportsQueueDepth pins the headline contract
// from issue #10: /api/health/full returns pending job counts
// when async ingest is enabled.
func TestHealthFull_ReportsQueueDepth(t *testing.T) {
	_, deps := setupHealthFullServer(t)

	// Stop the queue's workers so the worker pool doesn't
	// race the test by claiming the .json files before the
	// HTTP request fires. With workers stopped, files we
	// write directly to the persistence dir stay at status=
	// pending where Depth() can count them.
	deps.queue.Stop()

	// Write two pending files directly. JSON shape matches
	// what jobs.Queue writes so getFromPath round-trips.
	for _, id := range []string{"job-a", "job-b"} {
		writePendingJob(t, deps.queue.Dir(), id)
	}

	resp := doHealthFull(t, deps.server)

	t.Logf("response code: %d", resp.Code)
	if resp.Code != http.StatusOK {
		var raw map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&raw)
		t.Logf("response body: %+v", raw)
	}

	require.Equal(t, http.StatusOK, resp.Code)
	body := decodeHealthFull(t, resp)

	assert.True(t, body.Queue.Enabled,
		"queue should be reported as enabled when AsyncIngestQueueDir is set")
	assert.Equal(t, 2, body.Queue.Pending,
		"two pending jobs on disk; both must be counted")
	assert.Equal(t, 0, body.Queue.Failed)
}

// TestHealthFull_ReportsDisabledWhenNoQueue pins the no-async
// path: when AsyncIngestQueueDir is empty, ingest_queue.enabled
// is false.
func TestHealthFull_ReportsDisabledWhenNoQueue(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AsyncIngestQueueDir = "" // disable async

	svc := &fakeService{checkHealthOK: true}
	s := NewServer(cfg, svc)

	resp := doHealthFull(t, s)

	require.Equal(t, http.StatusOK, resp.Code)
	body := decodeHealthFull(t, resp)

	assert.False(t, body.Queue.Enabled,
		"queue should be reported as disabled when AsyncIngestQueueDir is empty")
}

// TestHealthFull_503WhenVectorDBUnhealthy pins that the basic
// health failure path still returns 503 from /api/health/full.
func TestHealthFull_503WhenVectorDBUnhealthy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AsyncIngestQueueDir = ""

	svc := &fakeService{checkHealthOK: false} // explicitly unhealthy

	s := NewServer(cfg, svc)

	w := doHealthFullRaw(t, s)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// --- helpers ---

// healthFullDeps exposes the queue reference tests need
// alongside the server.
type healthFullDeps struct {
	server *Server
	queue  *jobs.Queue
}

// setupHealthFullServer creates a NewServer with async ingest
// pointed at a temp directory. The queue is auto-created and
// started by NewServer; tests can submit jobs through deps.queue.
func setupHealthFullServer(t *testing.T) (*Server, *healthFullDeps) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Server.AsyncIngestQueueDir = dir
	// Disable the background cleanup sweep so tests can
	// assert on raw counts without races with the sweep.
	cfg.Server.AsyncIngestCleanupInterval = 0
	cfg.Server.AsyncIngestCompletedJobTTL = 0
	cfg.Server.AsyncIngestFailedJobTTL = 0

	s := NewServer(cfg, &fakeService{checkHealthOK: true})

	return s, &healthFullDeps{
		server: s,
		queue:  s.ingestQueue,
	}
}

// healthFullBody mirrors the JSON shape the endpoint returns.
// New fields land here as the endpoint evolves; tests assert
// against this struct rather than raw map[string]any so a
// missing field is a compile error, not a silent failure.
type healthFullBody struct {
	Status string `json:"status"`
	Queue  struct {
		Enabled     bool  `json:"enabled"`
		Workers     int   `json:"workers"`
		Pending     int   `json:"pending"`
		Processing  int   `json:"processing"`
		Completed   int   `json:"completed"`
		Failed      int   `json:"failed"`
		OldestAgeMs int64 `json:"oldest_pending_age_ms,omitempty"`
	} `json:"ingest_queue"`
}

func decodeHealthFull(t *testing.T, w *httptest.ResponseRecorder) healthFullBody {
	t.Helper()
	var body healthFullBody
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body),
		"response body must be valid JSON")
	return body
}

func doHealthFull(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/api/health/full", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func doHealthFullRaw(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	return doHealthFull(t, s)
}

// writePendingJob writes a synthetic pending job file at
// <dir>/<id>.json. Used by the queue-depth tests because the
// fake service's IngestDocument is fast enough that workers
// race the HTTP request before /api/health/full fires — the
// tests stop the queue's workers first, then seed pending
// files directly so Depth() sees them.
func writePendingJob(t *testing.T, dir, id string) {
	t.Helper()
	j := &jobs.Job{
		ID:        id,
		Content:   id,
		Status:    jobs.StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(j)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dir+"/"+id+".json", data, 0o600))
}
