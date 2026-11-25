package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postIngestFile uploads content as the "file" part of POST
// /api/ingest/file and returns the status code and the response
// body. The filename is the one docbuilder sends (the document's
// path), so the log-line assertions can pin it.
func postIngestFile(t *testing.T, router http.Handler, filename string, content []byte) (int, string) {
	t.Helper()

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/api/ingest/file", &b)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(body)
}

// TestHumaAPI_IngestFile_DocumentErrorIs400 pins the status split
// for the endpoint docbuilder pushes to: a document the server
// refuses because of its content is the caller's fault, so it
// answers 400 and says what to fix.
func TestHumaAPI_IngestFile_DocumentErrorIs400(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		ingestErr: models.InvalidDocument(errors.New("document UID is required")),
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	status, body := postIngestFile(t, router, "docs/intro.md", []byte("---\nuid: intro\n---\n\n# Title\nHi\n"))
	require.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, body, "document UID is required",
		"a 400 must tell the caller what to fix")
}

// TestHumaAPI_IngestFile_ServerErrorIs500 pins the other half of
// the split: an infrastructure failure (embeddings server down,
// vector store refused the write) is the server's fault, so it
// answers 500 — and the internal error stays out of the response.
func TestHumaAPI_IngestFile_ServerErrorIs500(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		ingestErr: errors.New("failed to generate embeddings: connection refused"),
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	status, body := postIngestFile(t, router, "docs/intro.md", []byte("---\nuid: intro\n---\n\n# Title\nHi\n"))
	require.Equal(t, http.StatusInternalServerError, status)
	assert.NotContains(t, body, "connection refused",
		"a 500 must not leak the internal failure to the caller")
}

// TestHumaAPI_IngestFile_LogsTheFailingDocument pins the log
// contract: an operator has to be able to find the failing
// document from the log line alone — which endpoint, which
// document, and whether the document or the server is at fault.
func TestHumaAPI_IngestFile_LogsTheFailingDocument(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		ingestErr: errors.New("failed to generate embeddings: connection refused"),
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	logBuf := captureLog(t)

	status, _ := postIngestFile(t, router, "docs/intro.md", []byte("---\nuid: intro\n---\n\n# Title\nHi\n"))
	require.Equal(t, http.StatusInternalServerError, status)

	logs := logBuf.String()
	assert.Contains(t, logs, "ingest-file", "the log must name the endpoint")
	assert.Contains(t, logs, `uid="intro"`, "the log must name the document")
	// Go's multipart reader strips the directory from the
	// uploaded filename, so the log carries the base name.
	assert.Contains(t, logs, `file="intro.md"`, "the log must name the uploaded file")
	assert.Contains(t, logs, "ingest failed",
		"the log must distinguish a server-side failure from a rejected document")
	assert.Contains(t, logs, "connection refused",
		"the log must carry the underlying error")
}

// TestHumaAPI_Ingest_DocumentErrorIs400 pins the same split on
// the JSON endpoint — both share the ingestFailure helper.
func TestHumaAPI_Ingest_DocumentErrorIs400(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		ingestErr: models.InvalidDocument(errors.New("frontmatter delimiter not found")),
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/ingest", map[string]any{"content": "---\nuid: intro\n\n# Title\n"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "frontmatter delimiter not found")
}

// TestHumaAPI_Ingest_ServerErrorIs500 pins the other half on the
// JSON endpoint.
func TestHumaAPI_Ingest_ServerErrorIs500(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		ingestErr: errors.New("failed to store document chunks: disk full"),
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/ingest", map[string]any{"content": "---\nuid: intro\n---\n\n# Title\n"})
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "disk full")
}

// TestHumaAPI_IngestGitLabIssue_DocumentErrorIs400 pins the same
// split on the GitLab sender endpoint: a rejected document is the
// sender's fault (400), not the server's.
func TestHumaAPI_IngestGitLabIssue_DocumentErrorIs400(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		ingestErr: models.InvalidDocument(errors.New("document UID is required")),
	}
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
		"path_with_namespace": "group/bar"
	}`)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/ingest/gitlab/issue", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
