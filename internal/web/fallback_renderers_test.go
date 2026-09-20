package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestFallbackRenderers_EscapeUserControlledStrings pins the
// security requirement that every page rendered via the
// serveBasicHTML fallback (chat.html, ingest.html,
// ingest_success.html, documents.html) MUST HTML-escape
// user-controlled strings before they hit the page. The
// fallback fires when the embedded template set does not
// include the requested page name — which is the production
// Docker image today, where only chat_message.html,
// search.html, and search_results.html ship in the embed.
//
// The XSS threat: doc.Title (extracted from the first H1
// header in markdown) and doc.Tags (taken verbatim from the
// `tags:` YAML frontmatter array) are attacker-controllable.
// A document with `tags: ["<img src=x onerror=alert(1)>"]`
// must NOT execute JS when the operator visits /documents.
//
// This test exercises the unsafe fallback on purpose by
// pointing the server at an empty templates directory, which
// forces every page through serveBasicHTML.
func TestFallbackRenderers_EscapeUserControlledStrings(t *testing.T) {
	t.Run("documents page escapes title, tags, category", func(t *testing.T) {
		cfg := config.DefaultConfig()
		// Force the empty-template fallback: empty TemplatesDir
		// loads from the embedded FS, but we then drop the
		// templates to simulate a deploy where the embed is
		// missing the documents.html page.
		cfg.Paths.TemplatesDir = ""

		maliciousTitle := "<script>alert(1)</script>"
		maliciousTag := "<img src=x onerror=alert(2)>"
		maliciousCategory := "\"><script>alert(3)</script>"

		svc := &fakeService{}
		s := NewServer(cfg, svc)

		// Drop embedded templates so serveBasicHTML fires for
		// every page. The point of this test is that the
		// fallback path is also safe, not just the template path.
		s.templates = nil

		// Override the service to return a malicious document
		// for ListDocuments.
		svc.documents = []models.DocumentInfo{
			{
				ID:         "doc-1",
				Title:      maliciousTitle,
				Tags:       []string{maliciousTag},
				Categories: []string{maliciousCategory},
				ChunkCount: 3,
			},
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()

		// The raw attack payload must not appear unescaped in
		// the response body. html/template escaping converts
		// `<script>` to `&lt;script&gt;` and `"` to `&#34;`.
		require.NotContains(t, body, maliciousTitle,
			"un-escaped title in /documents body — XSS in fallback renderer")
		require.NotContains(t, body, maliciousTag,
			"un-escaped tag in /documents body — XSS in fallback renderer")
		require.NotContains(t, body, maliciousCategory,
			"un-escaped category in /documents body — XSS in fallback renderer")

		// Sanity: the escaped form must be present (otherwise
		// the document was just dropped).
		require.Contains(t, body, "&lt;script&gt;",
			"escaped title missing from /documents body")
	})

	t.Run("ingest success page escapes document id and tags", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Paths.TemplatesDir = ""

		maliciousTag := "<img src=x onerror=alert(1)>"

		svc := &fakeService{
			ingestDocument: &models.Document{
				ID:   "doc-<script>alert(1)</script>",
				Tags: []string{maliciousTag},
				Chunks: []models.Chunk{
					{}, {}, {},
				},
			},
		}
		s := NewServer(cfg, svc)
		s.templates = nil

		form := strings.NewReader("content=hello")
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()

		require.NotContains(t, body, "<script>alert(1)</script>",
			"un-escaped document id in ingest_success body — XSS in fallback renderer")
		require.NotContains(t, body, "<img src=x onerror=alert(1)>",
			"un-escaped tag in ingest_success body — XSS in fallback renderer")
	})
}
