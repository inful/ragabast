package web

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/parser"
	"gopkg.in/yaml.v3"
)

// logIngestBodyPreviewBytes caps the body preview that's logged
// alongside a rejected ingest. 256 bytes is enough to see a
// trailing-comma mistake or a missing field, and small enough
// that the line stays readable in journald / stdout-piped logs.
// Anything larger than this is unlikely to help an operator
// diagnose — the error message itself already points at the
// problem.
const logIngestBodyPreviewBytes = 256

// ingestDocRef identifies the document an ingest request carried.
// Every field is best-effort: the uid comes from the frontmatter
// (empty when the frontmatter is what failed to parse) and the
// filename is only known on the multipart endpoint.
type ingestDocRef struct {
	UID  string // document uid, read from the frontmatter
	File string // uploaded filename, when the endpoint has one
	Body []byte // the document body, previewed in the log line
}

// logIngestError writes a single server-side log line for a
// failed ingest. The line carries:
//
//   - source (which endpoint)
//   - kind (what went wrong)
//   - the auth label from the request context, so operators
//     running senders under multiple bearer tokens can tell which
//     sender is failing
//   - the document reference (uid, filename, size)
//   - the underlying error, when present
//   - a truncated body preview, so the operator can see what the
//     document actually looked like
//
// The full detail goes to the server log; the response body
// receives only what the caller needs to fix their bug. This split
// mirrors the internalError pattern: log everything, leak only
// what the caller needs.
//
// `err` may be nil (the empty-body case). `doc.Body` may be nil
// or empty; the helper handles both without producing an
// unreadable preview.
func logIngestError(ctx context.Context, source, kind string, err error, doc ingestDocRef) {
	label := AuthLabelFromContext(ctx)
	preview := ingestBodyPreview(doc.Body)

	if err != nil {
		log.Printf("%s: %s label=%q uid=%q file=%q bytes=%d err=%q body=%s",
			source, kind, label, doc.UID, doc.File, len(doc.Body), err.Error(), preview)

		return
	}
	log.Printf("%s: %s label=%q uid=%q file=%q bytes=%d body=%s",
		source, kind, label, doc.UID, doc.File, len(doc.Body), preview)
}

// ingestBodyPreview returns a short preview of a request body for
// the log line. The preview keeps the whitespace of the original
// so a broken frontmatter stays readable. Only the first
// logIngestBodyPreviewBytes are copied — a failed ingest can carry
// a megabyte of document and the log line must not.
func ingestBodyPreview(body []byte) string {
	truncated := len(body) > logIngestBodyPreviewBytes
	if truncated {
		body = body[:logIngestBodyPreviewBytes]
	}
	preview := strings.TrimSpace(string(body))
	if truncated {
		preview += "..."
	}
	return preview
}

// ingestDocRefFromContent builds the log reference for a document
// body: its uid (read leniently from the frontmatter) and the body
// itself. A document whose frontmatter is broken yields an empty
// uid — the error message says why.
func ingestDocRefFromContent(content []byte, file string) ingestDocRef {
	ref := ingestDocRef{File: file, Body: content}

	fm, _, ok := parser.SplitDocbuilderFrontmatter(content)
	if !ok {
		return ref
	}
	var fields map[string]any
	if err := yaml.Unmarshal(fm, &fields); err != nil {
		return ref
	}
	uid, _ := fields["uid"].(string)
	ref.UID = strings.TrimSpace(uid)

	return ref
}

// ingestFailure maps a service-layer ingest error to an HTTP
// status and logs it.
//
// A document the server refuses — unparsable frontmatter, invalid
// YAML, a missing required field — is the caller's fault: 400 with
// the reason in the response body, because the caller can fix it.
// Everything else (the embeddings server is down, the vector store
// refused the write) is the server's fault: 500 with a generic
// message, because the caller cannot fix it and the details
// belong in the server log rather than in an HTTP response.
func ingestFailure(ctx context.Context, source string, err error, doc ingestDocRef) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, models.ErrInvalidDocument) {
		logIngestError(ctx, source, "invalid document", err, doc)
		return huma.Error400BadRequest("failed to ingest: " + err.Error())
	}

	logIngestError(ctx, source, "ingest failed", err, doc)
	return huma.Error500InternalServerError("failed to ingest: internal error")
}
