package web

import (
	"bytes"
	"context"
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
func registerIngestOperations(api huma.API, svc serviceAPI, limiter *IngestLimiter) {
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

		doc, err := svc.IngestDocument(ctx, content)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
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

		doc, err := svc.IngestDocument(ctx, string(input.RawBody))
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
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

		doc, err := svc.IngestDocument(ctx, string(b))
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
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
