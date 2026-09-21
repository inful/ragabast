package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/web/jobs"
)

// ingestAsyncRequestBody is the JSON shape POSTed to
// /api/ingest/async.
type ingestAsyncRequestBody struct {
	Content string `doc:"Docbuilder document content (including YAML frontmatter)." json:"content"`
}

// ingestAsyncResponseBody is the JSON shape returned from
// /api/ingest/async. Status is always "pending" at this
// point — the actual processing happens in a background
// worker and the caller polls the status endpoint.
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
func registerIngestJobsOperations(api huma.API, q *jobs.Queue) {
	if q == nil {
		// Async ingest disabled — register no-op handlers
		// that return 503 so callers get a clear error.
		registerIngestJobsDisabled(api)
		return
	}

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
		j, err := q.Submit(input.Body.Content, clientIPFromContext(ctx))
		if err != nil {
			if errors.Is(err, jobs.ErrJobContentTooLarge) {
				return nil, &huma.ErrorModel{
					Status: http.StatusRequestEntityTooLarge,
					Title:  http.StatusText(http.StatusRequestEntityTooLarge),
					Detail: "document exceeds server.max_ingest_document_bytes",
				}
			}
			return nil, huma.Error500InternalServerError("failed to submit job")
		}

		// 202 Accepted — return immediately, the worker
		// pool picks the job up asynchronously.
		return &struct{ Body ingestAsyncResponseBody }{Body: ingestAsyncResponseBody{
			JobID:     j.ID,
			Status:    string(jobs.StatusPending),
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
		j, err := q.Get(input.JobID)
		if err != nil {
			if errors.Is(err, jobs.ErrJobNotFound) {
				return nil, huma.Error404NotFound("job not found")
			}
			return nil, huma.Error500InternalServerError("failed to read job")
		}

		resp := &struct{ Body ingestJobResponseBody }{Body: ingestJobResponseBody{
			JobID:     j.ID,
			Status:    string(j.Status),
			Error:     j.Error,
			CreatedAt: j.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}}
		if j.DocumentID != "" {
			resp.Body.DocumentID = j.DocumentID
		}
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
}

// clientIPFromContext extracts the client IP from the
// request context if a chi request passed through the
// clientIPMiddleware; otherwise returns "unknown". The async
// path uses this only for the audit log — the actual
// rate-limit middleware sees the chi request directly.
func clientIPFromContext(ctx context.Context) string {
	return ClientIPFromContext(ctx)
}
