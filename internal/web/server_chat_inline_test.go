package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// TestChatMessage_LinksAreRenderedAsClickableAnchors pins the
// headline behavior: when the LLM reply contains [src:N] markers
// and the service is configured with a docbuilder base URL,
// InlineSourceLinks rewrites those to markdown links, the markdown
// renderer turns them into <a href=...> tags, the chat template
// drops them into the page as template.HTML (unescaped), and the
// final rendered HTML has clickable anchors.
//
// This test exists because the user has reported seeing the LLM's
// doc titles in their chat panel but NOT as clickable links. We
// verify the standard template + handler chain produces the link
// correctly in the unit test; if the user's custom template
// differs, this test still pins what the standard pipeline emits.
func TestChatMessage_LinksAreRenderedAsClickableAnchors(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "https://docs.example.com"
	svc := &fakeService{
		queryAnswer: "The main use case is aggregating docs [src:0] and [src:1].",
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					ChunkID:       "c0",
					DocumentID:    "d0",
					DocumentTitle: "Getting Started",
					UID:           "getting-started",
					Similarity:    0.9,
					DocbuilderURL: "https://docs.example.com/_uid/getting-started/",
				},
				{
					ChunkID:       "c1",
					DocumentID:    "d1",
					DocumentTitle: "Architecture",
					UID:           "architecture",
					Similarity:    0.8,
					DocbuilderURL: "https://docs.example.com/_uid/architecture/",
				},
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("message", "what is the main use case")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The literal "[src:0]" and "[src:1]" markers must NOT
	// appear in the rendered HTML — InlineSourceLinks is
	// supposed to have replaced them with [text](url).
	require.NotContains(t, body, "[src:0]",
		"raw [src:0] marker must not reach the rendered HTML")
	require.NotContains(t, body, "[src:1]",
		"raw [src:1] marker must not reach the rendered HTML")

	// The docbuilder URLs from the sources must appear in the
	// rendered HTML as anchor href attributes.
	require.Contains(t, body, `href="https://docs.example.com/_uid/getting-started/"`,
		"the [src:0] source's docbuilder URL must surface as an anchor href")
	require.Contains(t, body, `href="https://docs.example.com/_uid/architecture/"`,
		"the [src:1] source's docbuilder URL must surface as an anchor href")

	// The link text is the document title (from the source's
	// DocumentTitle field), not the raw "[src:0]" marker.
	require.Contains(t, body, ">Getting Started<",
		"link text should be the document title, not the [src:N] marker")
	require.Contains(t, body, ">Architecture<",
		"link text should be the document title, not the [src:N] marker")

	// Anchors open in a new tab with noopener, per
	// addTargetBlankToAnchors. bluemonday's UGCPolicy also
	// appends nofollow to anchor rel attributes, so the final
	// rel value is "noopener noreferrer nofollow" — assert on
	// the security-relevant tokens rather than the exact
	// string to stay robust against future sanitizer changes.
	require.Contains(t, body, `target="_blank"`,
		"anchors must open in a new tab")
	require.Contains(t, body, "noopener",
		"anchors must carry noopener to prevent tab-nabbing")
	require.Contains(t, body, "noreferrer",
		"anchors must carry noreferrer to avoid leaking the Referer header")
}

// TestChatMessage_HandlesModelsWithNativeThinkingTokens pins
// the case the user just hit: the model emits both <think>...</think>
// (its own thinking tokens) and [src:N] (its citations) in the
// SAME reply. The post-processors must strip the think block AND
// convert the [src:N] markers to links. Without StripThinkTags,
// the <think> content leaks into the visible reply and the user
// sees preamble; with InlineSourceLinks, the citations become
// real links.
func TestChatMessage_HandlesModelsWithNativeThinkingTokens(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = "https://docs.example.com"
	svc := &fakeService{
		queryAnswer: "<think>Let me look at the context.</think>\n\n" +
			"The answer is X [src:0].",
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					ChunkID:       "c0",
					DocumentID:    "d0",
					DocumentTitle: "Doc Title",
					UID:           "doc-title",
					Similarity:    0.9,
					DocbuilderURL: "https://docs.example.com/_uid/doc-title/",
				},
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("message", "test")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.NotContains(t, body, "Let me look",
		"the <think> block content must be stripped before rendering")
	require.NotContains(t, body, "<think>",
		"the <think> marker itself must be stripped")
	require.NotContains(t, body, "[src:0]",
		"the [src:N] marker must be replaced with a markdown link")
	require.Contains(t, body, `href="https://docs.example.com/_uid/doc-title/"`,
		"the docbuilder URL must surface as an anchor href")
	require.Contains(t, body, ">Doc Title<",
		"the link text should be the document title")
}

// keep import used.
var _ = strings.HasPrefix
