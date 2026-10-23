package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/gitlab"
)

// gitlabIssueIngestResponse mirrors ingestResponseBody so
// existing /api/ingest clients can read both with the same Go
// type. Huma generates one request/response type pair per
// operation; we own the response here and re-use the existing
// ingestResponseBody shape via composition.
type gitlabIssueIngestResponse struct {
	Body ingestResponseBody
}

// registerIngestGitLabIssueOperation wires the new endpoint that
// accepts a GitLab issue JSON envelope and pushes the result
// through the existing Service.IngestDocument pipeline.
//
// The endpoint is intentionally narrow: one envelope per request.
// A future bulk-ingest endpoint (issues: [...] form) would live
// in a separate handler so the wire shape and the per-issue
// checks stay obvious.
//
// Why this endpoint stays on the verbose huma.Register form
// (rather than registerPOST): the body is a JSON envelope we
// decode ourselves via internal/gitlab.EnvelopeToDocbuilderMarkdown,
// so the input is `[]byte` with `contentType:"*/*"` to defeat
// huma's default body-type inference. registerPOST's body
// wrapper expects a JSON-decodable struct, which doesn't fit.
// The /api/ingest/raw and /api/ingest/file endpoints in
// huma_ingest.go have the same carve-out for the same reason.
//
// maxDocumentBytes is the per-document size cap (from
// server.max_ingest_document_bytes), applied to the *rendered*
// markdown. The cap protects the embedding spend, which is
// downstream of the parser — bounding the rendered markdown is
// the right surface to guard.
func registerIngestGitLabIssueOperation(api huma.API, svc serviceAPI, limiter *IngestLimiter, maxDocumentBytes int) {
	huma.Register(api, huma.Operation{
		OperationID:  "ingest-gitlab-issue",
		Method:       http.MethodPost,
		Path:         "/api/ingest/gitlab/issue",
		Summary:      "Ingest a GitLab issue (sender fetches; ragabast is a pure receiver)",
		MaxBodyBytes: ingestBodyLimit(maxDocumentBytes),
	}, func(ctx context.Context, input *struct {
		RawBody []byte `contentType:"*/*" required:"true"`
	},
	) (*gitlabIssueIngestResponse, error) {
		// Decode the envelope ourselves — the internal/gitlab
		// package owns the full wire shape (including fields we
		// drop on the floor). Huma can't introspect
		// json.RawMessage fields, so a hand-rolled body type
		// would either reject extra GitLab fields or produce
		// a schema that's bigger than what we want to publish.
		body := input.RawBody
		if len(body) == 0 {
			logIngestError(ctx, "empty body", nil, body)

			return nil, huma.Error400BadRequest("request body is required")
		}

		var envelope gitlab.IssueEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			logIngestError(ctx, "invalid json", err, body)

			return nil, huma.Error400BadRequest(
				fmt.Sprintf("invalid GitLab issue envelope JSON: %s", err.Error()))
		}

		md, sysFiltered, err := gitlab.EnvelopeToDocbuilderMarkdown(envelope)
		if err != nil {
			logIngestError(ctx, "validation", err, body)

			return nil, huma.Error400BadRequest(
				fmt.Sprintf("invalid GitLab issue envelope: %s", err.Error()))
		}

		// Bound the rendered markdown before sending to the
		// embedding model. The same MaxIngestDocumentBytes cap
		// protects /api/ingest/raw; reusing it here keeps the
		// operator's "what's the biggest request I can send"
		// answer uniform across the two endpoints.
		//
		// withIngestGuard bundles the limiter acquire/release
		// with the size check so the four-line preamble
		// shared by every ingest endpoint shrinks to a
		// single call.
		release, err := withIngestGuard(limiter, maxDocumentBytes, len(md))
		if err != nil {
			return nil, err
		}
		defer release()

		// Operational feedback: the operator needs to know when
		// the sender's notes array had system records filtered
		// out, because that's data they thought they were pushing
		// and isn't in the embeddings. Tagged with both the
		// GitLab URL and the iid so logs from the sender side
		// can be correlated.
		if sysFiltered > 0 {
			log.Printf("gitlab: filtered %d system notes from issue %s (iid=%d)",
				sysFiltered, envelope.Issue.WebURL, envelope.Issue.IID)
		}

		doc, err := svc.IngestDocument(ctx, md)
		if err != nil {
			logIngestError(ctx, "ingest failed", err, body)

			return nil, huma.Error400BadRequest(
				fmt.Sprintf("failed to ingest: %s", err.Error()))
		}

		return &gitlabIssueIngestResponse{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})
}
