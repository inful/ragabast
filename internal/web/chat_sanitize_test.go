package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// TestHandleChatMessage_SanitizesReply pins the security
// requirement that the assistant's reply (which is rendered
// into the chat page via {{ .AnswerHTML }} wrapped as
// template.HTML) MUST NOT contain live script tags, event
// handlers, or javascript: URLs — regardless of what the
// upstream LLM or the markdown rendering pipeline emits.
//
// The current handler wraps the markdown-rendered reply as
// template.HTML after a single bluemonday UGCPolicy pass
// (see internal/web/markdown.go). A second bug in the
// markdown pipeline or in bluemonday itself would otherwise
// turn into stored/reflected XSS through the chat UI. This
// test fails today if any of those assumptions break, and
// passes once the handler adds a defense-in-depth sanitizer
// pass at the very end of the pipeline (after markdown, after
// inline-link rewriting, before template.HTML wrap).
func TestHandleChatMessage_SanitizesReply(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		bad   []string
	}{
		{
			name:  "script tag in plain reply is escaped",
			reply: "Hello <script>alert(1)</script> world",
			bad:   []string{"<script>alert(1)</script>"},
		},
		{
			name:  "img onerror in plain reply is stripped",
			reply: "Look <img src=x onerror=alert(2)> here",
			bad:   []string{"onerror=alert(2)"},
		},
		{
			name:  "javascript: URL in plain reply is stripped",
			reply: "Click [here](javascript:alert(3)) please",
			bad:   []string{"javascript:alert(3)"},
		},
		{
			name:  "iframe in plain reply is stripped",
			reply: "Visit <iframe src='https://evil.example'></iframe> soon",
			bad:   []string{"<iframe"},
		},
		{
			name:  "onclick attribute in plain reply is stripped",
			reply: "<a href='https://example.com' onclick='alert(4)'>link</a>",
			bad:   []string{"onclick="},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			svc := &fakeService{
				queryAnswer: tc.reply,
				queryDebug:  &service.QueryDebugInfo{Results: []models.SearchResult{}},
			}
			s := NewServer(cfg, svc)

			form := strings.NewReader("message=hi")
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", form)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()

			for _, bad := range tc.bad {
				require.NotContains(t, body, bad,
					"unsafe payload %q leaked into chat response — sanitization regressed", bad)
			}
		})
	}
}

// TestHandleChatMessage_FallbackEscapeStaysEscaped pins the
// behavior of the markdown-renderer-failure fallback path. If
// renderChatMarkdownToSafeHTML returns an error, the handler
// falls back to template.HTMLEscapeString on the raw reply
// before wrapping as template.HTML. The wrap as template.HTML
// is intentional because the markdown path needs the result
// trusted; the escape string fallback must therefore also be
// safe to wrap — i.e. it must remain escaped on output, not be
// re-interpreted as live HTML.
func TestHandleChatMessage_FallbackEscapeStaysEscaped(t *testing.T) {
	cfg := config.DefaultConfig()
	// A reply that contains raw HTML must still come back
	// escaped even if markdown rendering blew up (we cannot
	// exercise the failure path deterministically without
	// instrumenting the renderer; this test pins the
	// post-condition the fix must satisfy).
	malicious := "Hello <script>alert('xss')</script> world"
	svc := &fakeService{
		queryAnswer: malicious,
		queryDebug:  &service.QueryDebugInfo{Results: []models.SearchResult{}},
	}
	s := NewServer(cfg, svc)

	form := strings.NewReader("message=hi")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The raw payload must not survive unescaped in any
	// rendered fragment.
	require.NotContains(t, body, "<script>alert('xss')</script>",
		"raw script payload survived in chat response — fallback escape regressed")
}
