package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/reqid"
	"github.com/ragabast/internal/web/jobs"
)

// ingestAsyncRequestBody is the JSON shape POSTed to
// /api/ingest/async. Content is the docbuilder document,
// including its YAML frontmatter.
type ingestAsyncRequestBody struct {
	Content string `doc:"Docbuilder document content (including YAML frontmatter)." json:"content" required:"true"`
}

// ingestAsyncResponseBody is the 202-Accepted shape returned
// from /api/ingest/async. status_url points operators at the
// per-job polling endpoint.
type ingestAsyncResponseBody struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	StatusURL string `json:"status_url"`
}

// ingestJobResponseBody is the JSON shape returned from
// /api/ingest/jobs/{job_id}. Status mirrors the on-disk
// Job.Status; Chunks and DocumentID are populated only when
// Status == "completed".
type ingestJobResponseBody struct {
	JobID       string  `json:"job_id"`
	Status      string  `json:"status"`
	Error       string  `json:"error,omitempty"`
	DocumentID  string  `json:"document_id,omitempty"`
	Chunks      int     `json:"chunks,omitempty"`
	CreatedAt   string  `json:"created_at"`
	StartedAt   *string `json:"started_at,omitempty"`
	CompletedAt *string `json:"completed_at,omitempty"`
}

// ingestJobsListResponseBody is the envelope returned from
// GET /api/ingest/jobs. total reflects every job matching
// the status filter (not just the page), so callers can
// compute page counts for their UI.
type ingestJobsListResponseBody struct {
	Jobs  []ingestJobResponseBody `json:"jobs"`
	Total int                     `json:"total"`
}

// batchIngestMuxRegistrar is the slice of mux methods
// we need from the http.Handler passed to
// registerIngestJobsOperations. The interface covers the
// chi.HandleFunc signature (no method filter); we do the
// POST check in the handler itself so this single
// signature works for both chi (used by NewServer in
// production) and flow.Mux (used by humatest.New).
type batchIngestMuxRegistrar interface {
	HandleFunc(pattern string, fn http.HandlerFunc)
}

// batchIngestMuxWithMethodFilter is the wider signature
// used by huma's flow.Mux. If the router matches this
// shape we use it to register POST-only at the mux level
// (cheaper than a per-request method check).
type batchIngestMuxWithMethodFilter interface {
	HandleFunc(pattern string, fn http.HandlerFunc, methods ...string)
}

// registerIngestBatchHandler mounts POST /api/ingest/batch
// on the given mux (the chi router underneath huma). It's
// NOT registered via huma.Register because the body can
// be either a JSON array OR NDJSON — huma's body binding
// expects a single fixed schema and can't represent the
// "either shape" dispatch we need.
//
// The dispatch happens in parseIngestBatchBody, which
// looks at the first non-whitespace byte ('[' for array,
// '{' for NDJSON). Content-Type isn't required; the body
// shape is the source of truth.
func registerIngestBatchHandler(router http.Handler, q *jobs.Queue) {
	// Try the wider signature first (huma flow.Mux). If
	// that fails, fall back to the narrower one (chi).
	// This keeps the batch endpoint POST-only under flow
	// and method-checked under chi without two divergent
	// handler bodies.
	if mux, ok := router.(batchIngestMuxWithMethodFilter); ok {
		mux.HandleFunc("/api/ingest/batch", func(w http.ResponseWriter, r *http.Request) {
			handleIngestBatch(w, r, q)
		}, http.MethodPost)
		return
	}
	if mux, ok := router.(batchIngestMuxRegistrar); ok {
		mux.HandleFunc("/api/ingest/batch", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			handleIngestBatch(w, r, q)
		})
		return
	}
	panic("registerIngestBatchHandler: router does not support HandleFunc")
}

// ingestBatchItem is one entry in a batch POST body.
// Mirrors ingestAsyncRequestBody but in array/NDJSON form.
type ingestBatchItem struct {
	Content string `json:"content" required:"true"`
}

// ingestBatchRequest was the planned huma input struct
// for POST /api/ingest/batch. The endpoint ended up
// registered on the chi mux directly (see
// registerIngestBatchHandler) because huma's body
// binding couldn't represent the JSON-array-vs-NDJSON
// dispatch we need, so the struct is no longer used.
// Kept as a placeholder so a future refactor that goes
// back to huma.Register has a starting point — but
// unused for now.
type ingestBatchRequest struct { //nolint:unused
	RawBody []byte `body:"raw"`
}

// ingestBatchItemResponse is one element of the batch
// response array. status is \`queued\` for accepted docs
// (with a non-empty job_id) or \`error\` for rejected
// ones (with a non-empty error). Callers iterate the
// items array to learn which documents queued and which
// failed.
type ingestBatchItemResponse struct {
	Status     string `doc:"queued on success, error on failure" json:"status"`
	JobID      string `doc:"Set when status=queued" json:"job_id,omitempty"`
	DocumentID string `doc:"Set when status=queued" json:"document_id,omitempty"`
	Error      string `doc:"Set when status=error" json:"error,omitempty"`
}

// ingestBatchResponseBody is the envelope returned from
// POST /api/ingest/batch. items[i] corresponds to the
// i-th item in the request body, in order. The HTTP
// status is always 200 (or 4xx for batch-level errors
// like 413 for the whole batch exceeding the per-doc
// cap); per-document failures are reported as
// status=\"error\" in the items array.
type ingestBatchResponseBody struct {
	Items []ingestBatchItemResponse `json:"items"`
}

// registerIngestJobsOperations wires the async ingest
// endpoints. These complement (do NOT replace) the
// synchronous /api/ingest family: callers that need an
// immediate response still use /api/ingest; callers that
// want fire-and-forget ingestion (docbuilder's bulk-import
// path) use /api/ingest/async and poll /api/ingest/jobs/{id}.
//
// If the operator has not configured an ingest queue
// (server.async_ingest_queue_dir is empty), the async
// endpoints return 503 — the synchronous path remains
// available regardless.
func registerIngestJobsOperations(api huma.API, router http.Handler, q *jobs.Queue) {
	if q == nil {
		// Async ingest disabled — register no-op handlers
		// that return 503 so callers get a clear error.
		registerIngestJobsDisabled(api)
		return
	}

	// POST /api/ingest/batch is mounted on the chi router
	// directly (not via huma.Register) because it needs raw
	// body access for both JSON-array AND NDJSON formats.
	// huma's body binding expects a single JSON shape — it
	// can't represent "either an array or newline-delimited
	// objects". The dispatch happens here on body shape, so
	// Content-Type isn't required.
	registerIngestBatchHandler(router, q)

	huma.Register(api, huma.Operation{
		OperationID:   "ingest-async",
		Method:        http.MethodPost,
		Path:          "/api/ingest/async",
		Summary:       "Submit a docbuilder document for async ingest",
		Description:   "Returns 202 Accepted with a job ID immediately. The job is persisted to the async-ingest queue and processed by a background worker pool. Poll /api/ingest/jobs/{job_id} for status.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *struct{ Body ingestAsyncRequestBody }) (*struct{ Body ingestAsyncResponseBody }, error) {
		// Re-use the same per-document size check the sync
		// path enforces; ErrJobContentTooLarge is mapped to
		// 413 by the queue.
		j, err := q.Submit(input.Body.Content, clientIPFromContext(ctx), requestIDFromContext(ctx))
		if err != nil {
			if errors.Is(err, jobs.ErrJobContentTooLarge) {
				return nil, &huma.ErrorModel{
					Status: http.StatusRequestEntityTooLarge,
					Title:  http.StatusText(http.StatusRequestEntityTooLarge),
					Detail: "document exceeds server.max_ingest_document_bytes",
				}
			}
			return nil, huma.Error500InternalServerError("failed to submit job: " + err.Error())
		}
		return &struct{ Body ingestAsyncResponseBody }{Body: ingestAsyncResponseBody{
			JobID:     j.ID,
			Status:    string(j.Status),
			StatusURL: "/api/ingest/jobs/" + j.ID,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-job-status",
		Method:      http.MethodGet,
		Path:        "/api/ingest/jobs/{job_id}",
		Summary:     "Get the status of an async ingest job",
	}, func(ctx context.Context, input *struct {
		JobID string `path:"job_id"`
	},
	) (*struct{ Body ingestJobResponseBody }, error) {
		j, gerr := q.Get(input.JobID)
		if gerr != nil {
			if errors.Is(gerr, jobs.ErrJobNotFound) {
				return nil, huma.Error404NotFound("job not found")
			}
			return nil, huma.Error500InternalServerError("failed to read job: " + gerr.Error())
		}
		resp := &struct{ Body ingestJobResponseBody }{Body: buildJobResponseBody(j)}
		if j.Chunks > 0 {
			resp.Body.Chunks = j.Chunks
		}
		if j.StartedAt != nil {
			s := j.StartedAt.Format("2006-01-02T15:04:05Z07:00")
			resp.Body.StartedAt = &s
		}
		if j.CompletedAt != nil {
			s := j.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
			resp.Body.CompletedAt = &s
		}
		return resp, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-jobs-list",
		Method:      http.MethodGet,
		Path:        "/api/ingest/jobs",
		Summary:     "List async ingest jobs",
		Description: "Returns up to `limit` jobs (default 50) starting at `offset`, filtered by `status` if supplied. The `total` field reflects the count of all matching jobs, not just the page.",
	}, func(ctx context.Context, input *struct {
		Status string `enum:"pending,processing,completed,failed,all" query:"status,omitempty"`
		Limit  int    `maximum:"1000" minimum:"1" query:"limit,omitempty"`
		Offset int    `minimum:"0" query:"offset,omitempty"`
	},
	) (*struct{ Body ingestJobsListResponseBody }, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 50
		}
		offset := input.Offset

		filter := jobs.Status(input.Status)
		// "all" or empty means no filter.
		if filter == "all" || filter == "" {
			filter = ""
		}

		all, err := q.List(filter)
		if err != nil {
			return nil, huma.Error500InternalServerError(
				"failed to list jobs: " + err.Error())
		}

		// Page the slice. all is already filtered by status;
		// limit/offset just slice the result.
		total := len(all)
		if offset > total {
			offset = total
		}
		end := min(offset+limit, total)
		page := all[offset:end]

		out := make([]ingestJobResponseBody, len(page))
		for i, j := range page {
			out[i] = buildJobResponseBody(j)
		}
		return &struct{ Body ingestJobsListResponseBody }{
			Body: ingestJobsListResponseBody{Jobs: out, Total: total},
		}, nil
	})
}

// buildJobResponseBody converts a *jobs.Job to the wire-shape
// returned by /api/ingest/jobs/{job_id} and /api/ingest/jobs.
// Extracted from the per-job status handler so both endpoints
// stay in sync; a future field change here touches both
// callers automatically.
func buildJobResponseBody(j *jobs.Job) ingestJobResponseBody {
	resp := ingestJobResponseBody{
		JobID:     j.ID,
		Status:    string(j.Status),
		Error:     j.Error,
		CreatedAt: j.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if j.DocumentID != "" {
		resp.DocumentID = j.DocumentID
	}
	if j.Chunks > 0 {
		resp.Chunks = j.Chunks
	}
	if j.StartedAt != nil {
		s := j.StartedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.StartedAt = &s
	}
	if j.CompletedAt != nil {
		s := j.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.CompletedAt = &s
	}
	return resp
}

// registerIngestJobsDisabled wires the same endpoints but
// with handlers that return 503 Service Unavailable. Used
// when server.async_ingest_queue_dir is empty — the operator
// has opted out of async ingest, so the endpoints exist for
// API surface stability but signal "not configured" to
// callers.
func registerIngestJobsDisabled(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "ingest-async",
		Method:      http.MethodPost,
		Path:        "/api/ingest/async",
		Summary:     "Submit a docbuilder document for async ingest (disabled)",
	}, func(_ context.Context, _ *struct{ Body ingestAsyncRequestBody }) (*struct{ Body ingestAsyncResponseBody }, error) {
		return nil, huma.Error503ServiceUnavailable("async ingest is not configured (server.async_ingest_queue_dir is empty)")
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-job-status",
		Method:      http.MethodGet,
		Path:        "/api/ingest/jobs/{job_id}",
		Summary:     "Get the status of an async ingest job (disabled)",
	}, func(_ context.Context, _ *struct {
		JobID string `path:"job_id"`
	},
	) (*struct{ Body ingestJobResponseBody }, error) {
		return nil, huma.Error503ServiceUnavailable("async ingest is not configured (server.async_ingest_queue_dir is empty)")
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-jobs-list",
		Method:      http.MethodGet,
		Path:        "/api/ingest/jobs",
		Summary:     "List async ingest jobs (disabled)",
	}, func(_ context.Context, _ *struct{}) (*struct{ Body ingestJobsListResponseBody }, error) {
		return nil, huma.Error503ServiceUnavailable("async ingest is not configured (server.async_ingest_queue_dir is empty)")
	})
}

// clientIPFromContext extracts the client IP from the
// request context if a chi request passed through the
// clientIPMiddleware; otherwise returns "unknown". The async
// path uses this only for the audit log — the actual
// rate-limit middleware sees the chi request directly.
func clientIPFromContext(ctx context.Context) string {
	return ClientIPFromContext(ctx)
}

// requestIDFromContext returns the correlation ID for the
// current HTTP request (set by requestIDMiddleware) or ""
// when the call is not from the HTTP chain (CLI, tests).
// Used by the async-ingest endpoint to stamp Job.RequestID so
// the worker audit trail can be correlated with the request
// that submitted it. See issue #11.
func requestIDFromContext(ctx context.Context) string {
	return reqid.FromContext(ctx)
}
