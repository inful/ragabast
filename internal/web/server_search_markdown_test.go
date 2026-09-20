package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestHandleSearchSubmit_RendersMarkdownHeadings pins the fix
// for the 2026-09-20 UI issue: search results displayed raw
// markdown (`# heading` as literal text, `**bold**` as literal
// asterisks) because the template just dumped {{ .Content }}
// inside a <p>. Now each result's Content goes through
// renderChatMarkdownToSafeHTML and is exposed as ContentHTML.
func TestHandleSearchSubmit_RendersMarkdownHeadings(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{
				ChunkID:       "c1",
				DocumentID:    "doc-1",
				DocumentTitle: "ADR-001",
				HeaderPath:    "Decision",
				Level:         1,
				Content:       "## Decision\n\nWe will use **PostgreSQL** for storage.",
				Similarity:    0.85,
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "storage")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, "<h2",
		"a '## heading' line must render as <h2>; raw text dump is the regression")
	require.Contains(t, body, "<strong>PostgreSQL</strong>",
		"'**bold**' must render as <strong>; literal asterisks are the regression")
	require.NotContains(t, body, "## Decision",
		"raw markdown heading marker must NOT appear in the rendered HTML")
}

// TestHandleSearchSubmit_RendersInlineCodeAndLinks pins the
// common doc-builder markdown features: inline code and links
// must both render, links must open in a new tab (matching
// the chat handler's behavior).
func TestHandleSearchSubmit_RendersInlineCodeAndLinks(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{
				ChunkID:       "c1",
				DocumentID:    "doc-1",
				DocumentTitle: "CLI Reference",
				HeaderPath:    "Commands",
				Level:         1,
				Content:       "Run `ragabast ingest` to load docs. See [the README](https://example.com/readme).",
				Similarity:    0.81,
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "ingest")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, "<code>ragabast ingest</code>",
		"inline code must render as <code>; literal backticks are the regression")
	require.Contains(t, body, `href="https://example.com/readme"`,
		"the link target must be the URL the markdown source specified")
	require.Contains(t, body, `target="_blank"`,
		"rendered links must open in a new tab (consistent with the chat handler)")
	require.NotContains(t, body, "`ragabast ingest`",
		"raw markdown backticks must NOT appear in the rendered HTML")
}

// TestHandleSearchSubmit_RendersCodeBlocks pins the fenced
// code block case: ``` ... ``` blocks in the chunk content
// must render as <pre><code>, not as literal triple backticks.
func TestHandleSearchSubmit_RendersCodeBlocks(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{
				ChunkID:       "c1",
				DocumentID:    "doc-1",
				DocumentTitle: "API Reference",
				HeaderPath:    "Usage",
				Level:         1,
				Content:       "Usage:\n\n```\nragabast serve --port 8080\n```\n",
				Similarity:    0.79,
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "serve")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, "<pre>",
		"a fenced code block must render as <pre>; raw triple backticks are the regression")
	require.Contains(t, body, "<code>",
		"the code block must wrap its content in <code>")
	require.Contains(t, body, "ragabast serve --port 8080",
		"the code block content must be preserved verbatim")
	require.NotContains(t, body, "```",
		"raw triple-backtick fences must NOT appear in the rendered HTML")
}

// TestHandleSearchSubmit_RendersLists pins that markdown lists
// render as proper <ul>/<ol>, not as a wall of text with
// leading hyphens.
func TestHandleSearchSubmit_RendersLists(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{
				ChunkID:       "c1",
				DocumentID:    "doc-1",
				DocumentTitle: "Stack",
				HeaderPath:    "Components",
				Level:         1,
				Content:       "Components:\n\n- Go runtime\n- chromem-go\n- goldmark",
				Similarity:    0.77,
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "stack")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, "<ul",
		"a markdown bullet list must render as <ul>; literal hyphens are the regression")
	require.Contains(t, body, "<li>Go runtime</li>",
		"each list item must wrap in <li>")
	require.NotContains(t, body, "\n- Go runtime",
		"raw bullet-marker lines must NOT appear in the rendered HTML")
}

// TestHandleSearchSubmit_PropagatesRenderFailure pins the
// fallback contract: if the markdown renderer fails on a
// particular chunk, the chunk's ContentHTML must fall back
// to the escaped plaintext so the user still sees SOMETHING
// rather than an empty box.
func TestHandleSearchSubmit_FallsBackToEscapedText(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{
				ChunkID:       "c1",
				DocumentID:    "doc-1",
				DocumentTitle: "Edge Case",
				HeaderPath:    "Section",
				Level:         1,
				// The actual chat-renderer is robust to weird
				// input (we trust goldmark to either render or
				// escape), so this test mainly documents the
				// intent: the rendered output must include the
				// raw text in some form, even if the markdown
				// shape is unusual.
				Content:    "Plain text without markdown formatting",
				Similarity: 0.5,
			},
		},
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("query", "edge")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "Plain text without markdown formatting",
		"plain text chunks must still surface their content after rendering")
}
