package web

import (
	"context"
	"log"
	"strings"
)

// logIngestBodyPreviewBytes caps the body preview that's logged
// alongside a 400 from the gitlab ingest handler. 256 bytes is
// enough to see a trailing-comma mistake or a missing field, and
// small enough that the line stays readable in journald /
// stdout-piped logs. Anything larger than this is unlikely to
// help an operator diagnose — the parse error message itself
// already points at the byte offset.
const logIngestBodyPreviewBytes = 256

// logIngestError writes a single server-side log line for a
// 400 from the gitlab ingest endpoint. The line carries:
//
//   - the failure kind (empty body / invalid json / validation /
//     ingest failed) so an operator can grep one category at a
//     time
//   - the auth label from the request context, when set — so
//     operators running senders under multiple bearer tokens
//     can tell which sender is failing
//   - the underlying error, when present
//   - a truncated body preview (for empty/invalid-json cases
//     where the body itself is the problem)
//
// The full detail goes to the server log; the response body
// still receives the same sanitized message it always did, so a
// sender's error reporter sees actionable text. This split
// mirrors the internalError pattern: log everything, leak only
// what the caller needs to fix their bug.
//
// `err` may be nil for the empty-body case. `body` may be nil
// or empty; the helper handles both without producing an
// unreadable preview.
func logIngestError(ctx context.Context, kind string, err error, body []byte) {
	label := AuthLabelFromContext(ctx)

	var bodyPreview string
	if len(body) > 0 {
		preview := strings.TrimSpace(string(body))
		if len(preview) > logIngestBodyPreviewBytes {
			preview = preview[:logIngestBodyPreviewBytes] + "..."
		}
		bodyPreview = " body=" + preview
	}

	if err != nil {
		log.Printf("gitlab-ingest: %s label=%q err=%q%s",
			kind, label, err.Error(), bodyPreview)

		return
	}
	log.Printf("gitlab-ingest: %s label=%q%s",
		kind, label, bodyPreview)
}
