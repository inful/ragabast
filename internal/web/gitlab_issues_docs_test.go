package web

import (
	_ "embed"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// docsGitLabIssues is the rendered docs/gitlab-issues.md file at
// build time. The doc lives in the repo root; the relative path
// below is taken relative to this test file's directory at build
// time, which is internal/web/.
//
//go:embed testdata/gitlab-issues.md
var docsGitLabIssues string

// TestGitLabIssuesDocs_AuthHeaderContract pins the docs contract
// about how senders authenticate to /api/ingest/gitlab/issue.
//
// The auth middleware (internal/web/auth.go::matchBearer) reads
// only `Authorization: Bearer <token>` or the legacy
// `Authorization: Token <token>`. It does NOT read a bare
// `PRIVATE-TOKEN:` header — that is GitLab's own API convention,
// not ragabast's. A sender that copies the `PRIVATE-TOKEN:`
// header straight out of the GitLab docs into a ragabast call
// will reliably 401.
//
// The test fails if the docs/gitlab-issues.md recipe puts
// `RAGABAST_TOKEN` inside a `PRIVATE-TOKEN:` header. The fix is
// `Authorization: Bearer ${RAGABAST_TOKEN}`. The same docs file
// is allowed to keep `PRIVATE-TOKEN: ${GITLAB_TOKEN}` for the
// upstream-GitLab fetch calls — that pair is the right direction
// because GitLab really does accept that header. The dangerous
// pair is `PRIVATE-TOKEN:` carrying the `${RAGABAST_TOKEN}`
// variable, which would have the sender pass ragabast's auth via
// a header the middleware doesn't read.
//
// This test is the only thing standing between the docs and a
// future contributor who "fixes" the auth block to look more
// like GitLab's example. The docs are part of the API contract.
func TestGitLabIssuesDocs_AuthHeaderContract(t *testing.T) {
	text := docsGitLabIssues

	// Dangerous collocation: ragabast's auth variable appearing
	// in a header that ragabast's middleware doesn't read. Match
	// on the header name AND the variable name on the same line so
	// the test tolerates the upstream-GitLab `PRIVATE-TOKEN:
	// ${GITLAB_TOKEN}` lines, which are correct.
	for lineNo, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "PRIVATE-TOKEN") && strings.Contains(line, "RAGABAST_TOKEN") {
			t.Errorf("line %d: PRIVATE-TOKEN header carrying RAGABAST_TOKEN will 401 — use 'Authorization: Bearer ${RAGABAST_TOKEN}' instead:\n  %s",
				lineNo+1, strings.TrimSpace(line))
		}
	}

	// Positive contract — the docs MUST show the ragabast auth in
	// the right form. If a future rewrite drops the Bearer header
	// entirely, this catches it.
	require.Contains(t, text, "Authorization: Bearer ${RAGABAST_TOKEN}",
		"docs must show 'Authorization: Bearer ${RAGABAST_TOKEN}' for the ragabast POST — that's the only form authMiddleware accepts")

	// And the wire-shape block at the top of the doc must use the
	// right header too (not the in-code-block curl example — the
	// literal header name).
	require.Contains(t, text, "Authorization: Bearer <ragabast bearer token>",
		"docs wire-shape must show 'Authorization: Bearer <ragabast bearer token>' so a reader who only scans the top understands the auth")
}
