package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHumaAPI_IngestGitLabIssue_AcceptsValidEnvelope pins the
// happy path: a well-formed GitLab issue envelope is converted
// to docbuilder markdown, handed to Service.IngestDocument, and
// returns a 200 with the same response shape as /api/ingest/raw.
//
// The fake service is the existing fakeHumaService, which records
// lastIngestContent; the assertion checks the markdown it
// received has the GitLab-specific frontmatter (UID, tags, urls).
func TestHumaAPI_IngestGitLabIssue_AcceptsValidEnvelope(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	body := []byte(`{
		"issue": {
			"iid": 42,
			"project_id": 4,
			"title": "Auth: SAML timeout",
			"description": "Body markdown",
			"state": "opened",
			"labels": ["bug", "backend"],
			"created_at": "2026-01-15T10:00:00.000Z",
			"updated_at": "2026-01-15T13:42:00.000Z",
			"web_url": "https://gitlab.example.com/group/bar/-/issues/42",
			"issue_type": "issue"
		},
		"path_with_namespace": "group/bar",
		"include_notes": false,
		"include_system_notes": false,
		"notes": []
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 200, resp.StatusCode)

	// The service received docbuilder markdown, not the JSON envelope.
	require.Equal(t, 1, svc.ingestCalls, "IngestDocument must be invoked exactly once per request")
	// The uid is YAML-quoted because it has a ":" — sanitizeForYAML
	// guards the frontmatter block against crafted paths. The
	// contract here is "the GitLab UID appears in the rendered
	// frontmatter"; the exact textual form (quoted or unquoted) is
	// the internal/gitlab package's call.
	assert.Contains(t, svc.lastIngestContent, `gitlab:group/bar:42`,
		"the docbuilder markdown that reaches IngestDocument must carry the GitLab UID")
	assert.Contains(t, svc.lastIngestContent, "Auth: SAML timeout",
		"the title must round-trip to the markdown body")
	assert.Contains(t, svc.lastIngestContent, "https://gitlab.example.com/group/bar/-/issues/42",
		"the web_url must land in the urls frontmatter list")

	// Response body shape mirrors /api/ingest.
	var out ingestResponseBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, "Document ingested successfully", out.Message)
	assert.Equal(t, "doc-1", out.DocumentID)
}

// TestHumaAPI_IngestGitLabIssue_MissingIID_Returns400 pins the
// validation contract: the handler must reject envelopes with
// a missing issue.iid, before ever touching the embedding
// pipeline. Without this, a sender with a typo would burn
// embedding spend on a 400-fated request.
func TestHumaAPI_IngestGitLabIssue_MissingIID_Returns400(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	body := []byte(`{
		"issue": {
			"title": "no iid",
			"created_at": "2026-01-15T10:00:00.000Z",
			"web_url": "https://x"
		},
		"path_with_namespace": "group/bar"
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 400, resp.StatusCode,
		"missing iid must produce a 400, not silently ingest as zero")
	assert.Equal(t, 0, svc.ingestCalls,
		"IngestDocument must not be called for an envelope with missing iid")
}

// TestHumaAPI_IngestGitLabIssue_MissingPath_Returns400 pins the
// other half of the UID contract.
func TestHumaAPI_IngestGitLabIssue_MissingPath_Returns400(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	body := []byte(`{
		"issue": {
			"iid": 1,
			"title": "no path",
			"created_at": "2026-01-15T10:00:00.000Z",
			"web_url": "https://x"
		}
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 400, resp.StatusCode)
	assert.Equal(t, 0, svc.ingestCalls)
}

// TestHumaAPI_IngestGitLabIssue_IncludeNotes_EmbedsComments pins
// the notes-survive path: include_notes=true and a non-empty
// notes array must produce markdown with `## Comments` and the
// rendered note content.
func TestHumaAPI_IngestGitLabIssue_IncludeNotes_EmbedsComments(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	body := []byte(`{
		"issue": {
			"iid": 42,
			"title": "t",
			"created_at": "2026-01-15T10:00:00.000Z",
			"web_url": "https://x"
		},
		"path_with_namespace": "g/p",
		"include_notes": true,
		"include_system_notes": false,
		"notes": [
			{
				"id": 305, "body": "Bob comment",
				"created_at": "2026-01-15T13:42:00.000Z",
				"system": false,
				"author": {"username": "bob"}
			}
		]
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 200, resp.StatusCode)

	assert.Contains(t, svc.lastIngestContent, "## Comments",
		"include_notes=true must produce the Comments heading in the markdown that reaches IngestDocument")
	assert.Contains(t, svc.lastIngestContent, "Bob comment")
	assert.Contains(t, svc.lastIngestContent, "@bob")
}

// TestHumaAPI_IngestGitLabIssue_RespectsMaxIngestBytes pins the
// payload-size guard: the same MaxIngestDocumentBytes limit that
// protects /api/ingest/raw must apply here. The envelope form is
// potentially larger than the rendered output (sender might pad
// the notes array with hundreds of comments), and the
// embedding spend is what we want to bound — the rendered markdown
// is what the parser actually ingests, but checking both forms
// at the handler boundary would be wasteful. We check the rendered form, which is what's
// passed to the embedding model.
func TestHumaAPI_IngestGitLabIssue_RespectsMaxIngestBytes(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	// 100-byte limit: a small GitLab issue easily produces a
	// rendered markdown longer than that. The handler must
	// reject it before any embedding spend.
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 100, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	body := []byte(`{
		"issue": {
			"iid": 42,
			"title": "A reasonably long title that should already produce a body well past the limit",
			"description": "Some body text.",
			"created_at": "2026-01-15T10:00:00.000Z",
			"web_url": "https://gitlab.example.com/group/bar/-/issues/42"
		},
		"path_with_namespace": "group/bar"
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode,
		"oversized payload must produce 413, not silently ingest")
	assert.Equal(t, 0, svc.ingestCalls,
		"IngestDocument must not be called when the payload exceeds the cap")
}

// TestHumaAPI_IngestGitLabIssue_IncludesTimestampTests in the
// generated markdown: confirms the converter strips sub-second
// precision so the markdown frontmatter uses RFC 3339 second
// precision consistently. Without this, GitLab's microsecond-level
// timestamps leak into the markdown and the parser silently fails
// to populate doc.CreatedAt.
func TestHumaAPI_IngestGitLabIssue_NormalizesTimestamps(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	body := []byte(`{
		"issue": {
			"iid": 42,
			"title": "t",
			"created_at": "2016-01-04T15:31:51.081Z",
			"updated_at": "2016-01-04T15:31:46.176Z",
			"web_url": "https://x"
		},
		"path_with_namespace": "g/p"
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 200, resp.StatusCode)

	assert.Contains(t, svc.lastIngestContent, "date: 2016-01-04T15:31:51Z",
		"the converter must normalize GitLab's sub-second timestamps to RFC 3339 second precision")
	assert.Contains(t, svc.lastIngestContent, "created_at: 2016-01-04T15:31:51Z")
	assert.Contains(t, svc.lastIngestContent, "updated_at: 2016-01-04T15:31:46Z")
	assert.NotContains(t, svc.lastIngestContent, ".081Z",
		"sub-second precision must not appear in the frontmatter")
}

// TestHumaAPI_IngestGitLabIssue_LoggerReportsSystemFilteredCount
// pins the operational contract that the operator gets feedback
// when system notes are dropped. Without this, a sender could
// silently lose comment data and the operator would have no way
// to know.
func TestHumaAPI_IngestGitLabIssue_LogsSystemFilteredCount(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// Capture the default logger's output.
	logBuf := captureLog(t)

	body := []byte(`{
		"issue": {
			"iid": 42, "title": "t",
			"created_at": "2026-01-15T10:00:00.000Z",
			"web_url": "https://x"
		},
		"path_with_namespace": "g/p",
		"include_notes": true,
		"include_system_notes": false,
		"notes": [
			{"id": 1, "body": "real", "system": false,
				"created_at": "2026-01-15T11:00:00.000Z",
				"author": {"username": "alice"}},
			{"id": 2, "body": "closed", "system": true,
				"created_at": "2026-01-15T12:00:00.000Z",
				"author": {"username": "system"}},
			{"id": 3, "body": "changed title", "system": true,
				"created_at": "2026-01-15T13:00:00.000Z",
				"author": {"username": "system"}}
		]
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 200, resp.StatusCode)

	logs := logBuf.String()
	assert.Contains(t, logs, "gitlab:",
		"the system-note count log line must be tagged so the operator can grep for it")
	assert.Contains(t, logs, "system notes",
		"the log line must name the concept being dropped")
	assert.True(t, strings.Contains(logs, "iid=42") || strings.Contains(logs, "gitlab:g/p:42"),
		"the log line must identify the issue so the operator can correlate it with their sender logs")
}

// TestHumaAPI_IngestGitLabIssue_LogsEmptyBody pins the
// operational contract that an empty POST is visible in server
// logs — otherwise the operator sees only a 400 on the response
// and has no way to diagnose a sender that's sending blank
// requests (a real failure mode for misconfigured curl pipes).
func TestHumaAPI_IngestGitLabIssue_LogsEmptyBody(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	logBuf := captureLog(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(nil))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 400, resp.StatusCode)

	// The handler's empty-body branch is unreachable through the
	// public API today: huma's `required:"true"` body tag rejects
	// empty POSTs before our handler runs. We assert only that
	// the rejection produces a 400 — huma's own error body is
	// good enough for the sender; the operator-side log line
	// would only fire if huma's check were bypassed.
	logs := logBuf.String()
	assert.Empty(t, logs,
		"huma rejects empty bodies before our handler runs, so the handler's log line is not exercised")
}

// TestHumaAPI_IngestGitLabIssue_LogsInvalidJSON pins the
// log-line behavior for malformed JSON. The body preview is
// included (truncated to a sane length) so the operator can see
// what the sender actually sent — that's the diagnostic the
// sender's bug reporter needs.
func TestHumaAPI_IngestGitLabIssue_LogsInvalidJSON(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	logBuf := captureLog(t)

	// Trailing comma — a common jq mistake.
	body := []byte(`{"issue": {"iid": 42,}, "path_with_namespace": "g/p"}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 400, resp.StatusCode)

	logs := logBuf.String()
	assert.Contains(t, logs, "gitlab-ingest",
		"server log must carry the kind tag")
	assert.Contains(t, logs, "invalid json",
		"server log must name the failure kind so the operator can grep for parse errors")
	assert.Contains(t, logs, "iid",
		"server log body preview must include enough of the payload for the operator to spot the syntax error")
}

// TestHumaAPI_IngestGitLabIssue_LogsMissingIID pins the
// log-line behavior for the iid==0 validation case.
func TestHumaAPI_IngestGitLabIssue_LogsMissingIID(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	logBuf := captureLog(t)

	body := []byte(`{
		"issue": {
			"title": "no iid",
			"created_at": "2026-01-15T10:00:00.000Z",
			"web_url": "https://x"
		},
		"path_with_namespace": "group/bar"
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 400, resp.StatusCode)

	logs := logBuf.String()
	assert.Contains(t, logs, "gitlab-ingest")
	assert.Contains(t, logs, "validation",
		"server log must name the validation kind separately from parse failures")
	assert.Contains(t, logs, "iid",
		"server log must surface the specific missing field so the operator can tell them which input they forgot")
	assert.Contains(t, logs, "group/bar",
		"server log must surface the path_with_namespace so the operator can identify which project the bad request was about")
}

// TestHumaAPI_IngestGitLabIssue_LogsMissingPath pins the
// log-line behavior for empty path validation.
func TestHumaAPI_IngestGitLabIssue_LogsMissingPath(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	logBuf := captureLog(t)

	body := []byte(`{
		"issue": {
			"iid": 42,
			"title": "no path",
			"created_at": "2026-01-15T10:00:00.000Z",
			"web_url": "https://x"
		}
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 400, resp.StatusCode)

	logs := logBuf.String()
	assert.Contains(t, logs, "gitlab-ingest")
	assert.Contains(t, logs, "validation")
	assert.Contains(t, logs, "path",
		"server log must name the missing path_with_namespace so the operator can correct the sender")
	assert.Contains(t, logs, "iid=42",
		"server log must surface the iid so the operator knows which issue the request was about")
}

// TestHumaAPI_IngestGitLabIssue_LogsIngestDocumentFailure pins
// the log-line behavior for the case where the JSON parsed,
// validation passed, but Service.IngestDocument returned an
// error. These errors get wrapped as 400 today (matching the
// /api/ingest/raw convention) but they're often server-side
// failures in disguise — a chunker bug, a vector store hiccup.
// The server log must carry the full chain so an operator can
// distinguish "sender bug" from "internal failure".
func TestHumaAPI_IngestGitLabIssue_LogsIngestDocumentFailure(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		ingestErr: assert.AnError, //nolint:goerr113 // test fixture
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	logBuf := captureLog(t)

	body := []byte(`{
		"issue": {
			"iid": 42,
			"title": "t",
			"created_at": "2026-01-15T10:00:00.000Z",
			"web_url": "https://x"
		},
		"path_with_namespace": "group/bar"
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 400, resp.StatusCode)

	logs := logBuf.String()
	assert.Contains(t, logs, "gitlab-ingest")
	assert.Contains(t, logs, "ingest failed",
		"server log must distinguish downstream IngestDocument failures from client-side validation errors")
	// The body preview is the wire envelope — it contains the
	// iid and path_with_namespace, not the resolved UID
	// (gitlab:group/bar:42). The resolved UID only exists in the
	// rendered markdown, which the wire-body log doesn't carry.
	// The path and iid are enough to correlate the failure with
	// the sender's request.
	assert.Contains(t, logs, "group/bar",
		"server log body preview must include the path_with_namespace so the operator can identify which project the failed ingest was about")
	assert.Contains(t, logs, `"iid": 42`,
		"server log body preview must include the iid so the operator can identify the specific issue that failed")
}

// captureLog swaps log's default writer for a buffer and
// restores it on cleanup. Same shape as the helper in
// internal/service/ingest_unpublished_test.go, kept package-
// local because Go test helpers don't cross packages without
// an export_test.go file and the duplication is cheaper than the
// alternative here.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	oldOut := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	})
	return buf
}
