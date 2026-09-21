package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/ragabast/internal/reqid"
	"github.com/ragabast/internal/web/jobs"
)

// handleIngestBatch is the raw http.Handler for
// POST /api/ingest/batch. Lives outside the huma
// framework because the body format (JSON array vs
// NDJSON) can't be expressed as a single huma schema.
//
// Behavior:
//   - Reads the raw body, dispatches on body shape
//     (parseIngestBatchBody).
//   - For each successfully-parsed item, calls
//     jobs.Queue.Submit (same code path as
//     /api/ingest/async, so per-doc size cap and queue
//     stopped semantics apply).
//   - The HTTP response is always 200 unless the BODY
//     itself failed to parse (400). Per-item submission
//     failures are reported as status=error entries in
//     the items array — a partial failure is not a
//     request failure.
//
// The response is JSON-encodeable directly because the
// items slice is built in input order, so callers can
// correlate items[i] to the i-th doc they submitted.
func handleIngestBatch(w http.ResponseWriter, r *http.Request, q *jobs.Queue) {
	// Read the full body. The async-ingest queue enforces
	// per-doc size caps (ErrJobContentTooLarge), but the
	// total request body has no explicit cap — the
	// maxBytes middleware enforces a global 10 MiB limit
	// before this handler runs. For larger batches
	// operators stream via NDJSON anyway, so unbounded
	// total is fine.
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	items, err := parseIngestBatchBody(raw)
	if err != nil {
		http.Error(w, "malformed batch body: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := ingestBatchResponseBody{Items: make([]ingestBatchItemResponse, len(items))}
	for i, item := range items {
		j, subErr := q.Submit(item.Content, clientIPFromContext(r.Context()), reqid.FromContext(r.Context()))
		if subErr != nil {
			resp.Items[i] = ingestBatchItemResponse{
				Status: "error",
				Error:  subErr.Error(),
			}
			// Per the issue, ErrJobContentTooLarge is
			// surfaced as 413 only for the single-doc
			// endpoint. For batch, surface as a per-item
			// error so the caller can pinpoint which doc
			// was too big.
			if errors.Is(subErr, jobs.ErrJobContentTooLarge) {
				resp.Items[i].Error = "document exceeds server.max_ingest_document_bytes"
			}
			continue
		}
		resp.Items[i] = ingestBatchItemResponse{
			Status: "queued",
			JobID:  j.ID,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Already wrote headers — best-effort log only.
		// Can't change the status code at this point.
		return
	}
}
