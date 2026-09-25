package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// ingestRequestBody is the JSON shape POSTed to /api/ingest.
type ingestRequestBody struct {
	Content string `doc:"Docbuilder document content (including YAML frontmatter)." json:"content"`
}

// ingestResponseBody is the JSON shape returned from any of the
// /api/ingest, /api/ingest/raw, or /api/ingest/file endpoints.
type ingestResponseBody struct {
	Message    string `json:"message"`
	DocumentID string `json:"document_id"`
	Chunks     int    `json:"chunks"`
}

// registerIngestOperations wires the three ingest endpoints
// (JSON body, raw markdown body, multipart file upload). All
// three share the same limiter and the same response shape.
//
// maxDocumentBytes is the per-document size cap (from
// server.max_ingest_document_bytes). A 0 value disables the
// check — useful for tests but should not be set in
// production; the config default is 1 MiB.
func registerIngestOperations(api huma.API, svc serviceAPI, limiter *IngestLimiter, maxDocumentBytes int) {
	huma.Register(api, huma.Operation{
		OperationID: "ingest",
		Method:      http.MethodPost,
		Path:        "/api/ingest",
		Summary:     "Ingest a docbuilder document",
	}, func(ctx context.Context, input *struct{ Body ingestRequestBody }) (*struct{ Body ingestResponseBody }, error) {
		if !acquireIngestSlot(limiter) {
			return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests("ingest busy"), limiter.RetryAfterHeader())
		}
		if limiter != nil {
			defer limiter.Release()
		}

		content := strings.TrimSpace(input.Body.Content)
		if content == "" {
			return nil, huma.Error400BadRequest("content is required")
		}
		if err := checkIngestSize(len(content), maxDocumentBytes); err != nil {
			return nil, err
		}

		doc, err := svc.IngestDocument(ctx, content)
		if err != nil {
			return nil, huma.Error400BadRequest(
				fmt.Sprintf("failed to ingest: %s", err.Error()))
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-raw",
		Method:      http.MethodPost,
		Path:        "/api/ingest/raw",
		Summary:     "Ingest a docbuilder markdown document (raw body)",
	}, func(ctx context.Context, input *struct {
		RawBody []byte `contentType:"text/markdown" required:"true"`
	},
	) (*struct{ Body ingestResponseBody }, error) {
		if !acquireIngestSlot(limiter) {
			return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests("ingest busy"), limiter.RetryAfterHeader())
		}
		if limiter != nil {
			defer limiter.Release()
		}

		if len(bytes.TrimSpace(input.RawBody)) == 0 {
			return nil, huma.Error400BadRequest("request body is required")
		}
		if err := checkIngestSize(len(input.RawBody), maxDocumentBytes); err != nil {
			return nil, err
		}

		doc, err := svc.IngestDocument(ctx, string(input.RawBody))
		if err != nil {
			return nil, huma.Error400BadRequest(
				fmt.Sprintf("failed to ingest: %s", err.Error()))
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-file",
		Method:      http.MethodPost,
		Path:        "/api/ingest/file",
		Summary:     "Ingest a docbuilder markdown document (multipart upload)",
	}, func(ctx context.Context, input *struct {
		RawBody huma.MultipartFormFiles[struct {
			File huma.FormFile `form:"file" required:"true"`
		}]
	},
	) (*struct{ Body ingestResponseBody }, error) {
		if !acquireIngestSlot(limiter) {
			return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests("ingest busy"), limiter.RetryAfterHeader())
		}
		if limiter != nil {
			defer limiter.Release()
		}

		form := input.RawBody.Data()
		b, err := io.ReadAll(form.File)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to read uploaded file")
		}

		if len(bytes.TrimSpace(b)) == 0 {
			return nil, huma.Error400BadRequest("file is empty")
		}
		if sizeErr := checkIngestSize(len(b), maxDocumentBytes); sizeErr != nil {
			return nil, sizeErr
		}

		doc, err := svc.IngestDocument(ctx, string(b))
		if err != nil {
			return nil, huma.Error400BadRequest(
				fmt.Sprintf("failed to ingest: %s", err.Error()))
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})
}

// acquireIngestSlot reports whether the limiter allows another
// ingest. A nil limiter means "no rate limiting" and always
// returns true. Callers MUST defer limiter.Release() when this
// returns true AND limiter is non-nil.
func acquireIngestSlot(limiter *IngestLimiter) bool {
	if limiter == nil {
		return true
	}
	return limiter.TryAcquire()
}

// checkIngestSize returns a huma 413 error if the document
// content exceeds the configured per-document limit. A
// maxDocumentBytes of 0 disables the check (test-only path);
// production callers should always pass a non-zero value via
// server.max_ingest_document_bytes (default 1 MiB).
//
// The check happens BEFORE the embedding model is invoked, so
// the worst case is a single chunker pass over the rejected
// document — bounded work, no upstream spend.
//
// huma does not export an Error413 constructor, so we
// construct an *huma.ErrorModel with the right status. The
// Huma middleware extracts the status from ErrorModel.GetStatus
// and writes the matching HTTP response.
func checkIngestSize(contentLen, maxDocumentBytes int) error {
	if maxDocumentBytes <= 0 {
		return nil
	}
	if contentLen > maxDocumentBytes {
		return &huma.ErrorModel{
			Status: http.StatusRequestEntityTooLarge,
			Title:  http.StatusText(http.StatusRequestEntityTooLarge),
			Detail: "document exceeds server.max_ingest_document_bytes",
		}
	}
	return nil
}
