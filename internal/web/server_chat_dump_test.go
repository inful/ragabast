package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// TestChatMessage_DumpRenderedHTMLForUserInspection is a manual
// helper test, not a real assertion. When run with `-tags dump`,
// it writes the rendered chat-panel HTML to /tmp/rendered-chat.html
// so the user can inspect what ragabast's standard pipeline
// actually produces.
//
// Build tag requirement: `go test -tags dump` skips this unless
// explicitly requested; the default `go test ./...` does NOT
// dump a file in the user's /tmp.
//
// To run:
//
//	go test ./internal/web/ -tags dump -run TestChatMessage_DumpRenderedHTMLForUserInspection -v
//	cat /tmp/rendered-chat.html
func TestChatMessage_DumpRenderedHTMLForUserInspection(t *testing.T) {
	if !dumpEnabled {
		t.Skip("dump tag not set; re-run with -tags dump to dump the rendered chat HTML")
	}

	cfg := config.DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "https://docs.example.com"
	svc := &fakeService{
		queryAnswer: "The main use case for DocBuilder is aggregating documentation from multiple Git repositories into a unified Hugo static site [src:1]. It is a Go CLI tool and daemon designed to produce multi-repository Hugo documentation sites [src:0].",
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					ChunkID:       "c0",
					DocumentID:    "d0",
					DocumentTitle: "Getting Started with DocBuilder",
					UID:           "getting-started",
					Similarity:    0.76,
					DocbuilderURL: "https://docs.example.com/_uid/getting-started/",
				},
				{
					ChunkID:       "c1",
					DocumentID:    "d1",
					DocumentTitle: "Comprehensive Architecture Documentation",
					UID:           "comprehensive-architecture",
					Similarity:    0.75,
					DocbuilderURL: "https://docs.example.com/_uid/comprehensive-architecture/",
				},
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("message", "what is the main use case for docbuilder")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	out, err := os.Create("/tmp/rendered-chat.html")
	require.NoError(t, err)
	defer func() { _ = out.Close() }()
	_, err = out.WriteString("<!DOCTYPE html>\n<html><head><title>Rendered Chat</title></head>\n<body>\n")
	require.NoError(t, err)
	_, err = out.WriteString(body)
	require.NoError(t, err)
	_, err = out.WriteString("\n</body></html>")
	require.NoError(t, err)
	t.Logf("Wrote %d bytes to /tmp/rendered-chat.html", len(body))
}

// dumpEnabled is set by a build constraint in dump_enabled_test.go.
// We use a flag file rather than the canonical `testing.Short()`
// because this is a manual-inspection helper, not a CI guard.
var dumpEnabled = false

// keep the unused imports quiet when the test skips.
var _ = strings.HasPrefix
