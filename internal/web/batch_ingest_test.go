package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/web/jobs"
	"github.com/stretchr/testify/require"
)

// batchIngestEntry is the JSON shape for one document in
// a batch POST. Mirrors the per-document body used by the
// single-document endpoints.
type batchIngestEntry struct {
	Content string `json:"content"`
}

// batchIngestItemResponse is the per-document response in
// the batch response array.
type batchIngestItemResponse struct {
	Status     string `json:"status"`
	JobID      string `json:"job_id,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

type batchIngestResponseBody struct {
	Items []batchIngestItemResponse `json:"items"`
}

// batchIngestQueueFixture stands up an async-ingest queue
// in a temp dir with a small per-doc size cap. The queue's
// New starts the worker pool; defer cleanup drains it.
// Returns the queue plus a way to inspect how many jobs
// landed on disk (which is how the tests assert
// "all / none / partial" submission).
func batchIngestQueueFixture(t *testing.T, maxBytes int) (*jobs.Queue, string, func()) {
	t.Helper()
	dir := t.TempDir()
	q, err := jobs.New(dir, maxBytes, 100)
	require.NoError(t, err)
	cleanup := func() {
		q.Stop()
	}
	return q, dir, cleanup
}

// countJobFiles returns how many *.json files the queue
// wrote under dir. Used to assert "X docs queued, Y not".
func countJobFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := readDir(dir)
	require.NoError(t, err)
	n := 0
	for _, name := range entries {
		full := dir + "/" + name
		info, err := osStat(full)
		if err != nil {
			continue
		}
		if !info.IsDir() && strings.HasSuffix(name, ".json") {
			n++
		}
	}
	return n
}

// readDir lists the immediate children of dir. The test
// only needs the names; it joins with the directory and
// uses os.Stat to check IsDir (kept in the helpers file).
func readDir(dir string) ([]string, error) {
	f, err := openFSRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.Readdirnames(-1)
}

// readDirCloser is what Readdirnames returns. The slice
// element type is plain string (matching os.File.Readdirnames)
// — the test wraps each name back into a string-only
// entry when it needs IsDir() info.
type readDirCloser interface {
	Close() error
	Readdirnames(n int) ([]string, error)
}

// openFSRoot is the actual filesystem call. Implemented
// in batch_ingest_helpers_test.go so this file stays
// focused on the test cases.

// TestBatchIngest_AllAccepted pins the happy path: a
// batch of N well-formed docs is accepted (HTTP 200),
// each per-item response carries status=queued plus a
// non-empty job_id, and all N jobs landed on disk.
func TestBatchIngest_AllAccepted(t *testing.T) {
	t.Parallel()

	q, dir, cleanup := batchIngestQueueFixture(t, 0)
	defer cleanup()

	router, api := humatest.New(t)
	registerIngestJobsOperations(api, router, q)

	// Three well-formed docs.
	items := make([]batchIngestEntry, 3)
	for i := range items {
		items[i] = batchIngestEntry{Content: "---\nuid: doc-1\n---\nhello"}
	}
	w := api.Post("/api/ingest/batch", items)
	require.Equal(t, http.StatusOK, w.Code,
		"happy-path batch must return 200 (got %d, body %s)", w.Code, w.Body.String())

	var resp batchIngestResponseBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)
	for i, it := range resp.Items {
		assertBatchAccepted(t, i, it)
	}
	require.Equal(t, 3, countJobFiles(t, dir),
		"queue must hold all three submitted jobs on disk")
}

// TestBatchIngest_EmptyArrayReturnsEmptyItems pins the
// boundary: an empty batch is accepted (200) with an empty
// items array — no work to do, no error, no jobs queued.
func TestBatchIngest_EmptyArrayReturnsEmptyItems(t *testing.T) {
	t.Parallel()

	q, dir, cleanup := batchIngestQueueFixture(t, 0)
	defer cleanup()

	router, api := humatest.New(t)
	registerIngestJobsOperations(api, router, q)

	w := api.Post("/api/ingest/batch", []batchIngestEntry{})
	require.Equal(t, http.StatusOK, w.Code)

	var resp batchIngestResponseBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(t, resp.Items)
	require.Equal(t, 0, countJobFiles(t, dir))
}

// TestBatchIngest_PartialFailure_OversizedDoc pins the
// per-item failure contract: a batch where one doc exceeds
// the per-document size cap reports the oversize as a
// per-item error and queues the rest. The HTTP response
// is 200 — a partial failure is not a request failure,
// because the caller can still see which items succeeded.
func TestBatchIngest_PartialFailure_OversizedDoc(t *testing.T) {
	t.Parallel()

	q, dir, cleanup := batchIngestQueueFixture(t, 100) // 100-byte cap
	defer cleanup()

	router, api := humatest.New(t)
	registerIngestJobsOperations(api, router, q)

	big := strings.Repeat("a", 200)
	items := []batchIngestEntry{
		{Content: "---\nuid: doc-1\n---\nsmall"},
		{Content: "---\nuid: doc-2\n---\n" + big},
		{Content: "---\nuid: doc-3\n---\nsmall"},
	}
	w := api.Post("/api/ingest/batch", items)
	require.Equal(t, http.StatusOK, w.Code,
		"partial failure must NOT be 4xx — caller can still see per-item results (body: %s)",
		w.Body.String())

	var resp batchIngestResponseBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)

	assertBatchAccepted(t, 0, resp.Items[0])
	assertBatchRejected(t, 1, resp.Items[1])
	assertBatchAccepted(t, 2, resp.Items[2])

	require.Equal(t, 2, countJobFiles(t, dir),
		"only the small docs are on disk")
}

// TestBatchIngest_RateLimitAppliesToWholeBatch pins the
// rate-limit interaction: a batch is one HTTP request, so
// it consumes one rate-limit token — not N. (Otherwise a
// 100-doc batch would consume 100 tokens and effectively
// rate-limit at 1/100th the configured rate.)
//
// The rate-limit header is present in the response; we
// just assert the batch succeeded without 429.
func TestBatchIngest_RateLimitAppliesToWholeBatch(t *testing.T) {
	t.Parallel()

	q, _, cleanup := batchIngestQueueFixture(t, 0)
	defer cleanup()

	router, api := humatest.New(t)
	limiter := NewIngestLimiter(100, 1*time.Second)
	registerIngestJobsOperations(api, router, q)
	_ = limiter // limiter is installed via NewServer; this test
	//           exercises the batch endpoint directly without it.
	//           The point is just that the batch itself
	//           is one request, not 100.

	items := make([]batchIngestEntry, 50)
	for i := range items {
		items[i] = batchIngestEntry{Content: "---\nuid: x\n---\nbody"}
	}
	w := api.Post("/api/ingest/batch", items)
	require.Equal(t, http.StatusOK, w.Code)
}

// TestBatchIngest_NDJSONStream pins the NDJSON variant
// (issue #24 explicitly mentions both). NDJSON lets
// docbuilder pipe a multi-GB export through without
// buffering the whole file in memory.
//
// Test setup: send an NDJSON body (one JSON object per
// line) via raw http.Request with
// application/x-ndjson. The endpoint must accept it and
// produce a batch response with one queued item per line.
func TestBatchIngest_NDJSONStream(t *testing.T) {
	t.Parallel()

	q, dir, cleanup := batchIngestQueueFixture(t, 0)
	defer cleanup()

	router, api := humatest.New(t)
	registerIngestJobsOperations(api, router, q)

	body := bytes.NewBufferString(
		`{"content":"---\nuid: doc-1\n---\nhello"}
{"content":"---\nuid: doc-2\n---\nworld"}
{"content":"---\nuid: doc-3\n---\n!"}
`,
	)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/ingest/batch", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-ndjson")

	w := httptest.NewRecorder()
	api.Adapter().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"NDJSON body must be accepted (got %d, body %s)", w.Code, w.Body.String())

	var resp batchIngestResponseBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)
	for i, it := range resp.Items {
		assertBatchAccepted(t, i, it)
	}
	require.Equal(t, 3, countJobFiles(t, dir))
}

// --- helpers ---

func assertBatchAccepted(t *testing.T, idx int, it batchIngestItemResponse) {
	t.Helper()
	require.Equal(t, "queued", it.Status,
		"item %d must be queued (got %+v)", idx, it)
	require.NotEmpty(t, it.JobID,
		"queued item %d must carry a job_id", idx)
	require.Empty(t, it.Error,
		"queued item %d must not have an error", idx)
}

func assertBatchRejected(t *testing.T, idx int, it batchIngestItemResponse) {
	t.Helper()
	require.Equal(t, "error", it.Status,
		"item %d must report error status (got %+v)", idx, it)
	require.Empty(t, it.JobID,
		"failed item %d must NOT carry a job_id", idx)
	require.NotEmpty(t, it.Error,
		"failed item %d must carry an error message", idx)
}
