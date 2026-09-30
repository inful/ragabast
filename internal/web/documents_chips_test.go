package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestDocumentsFallback_RendersTagsAsChips pins the fix
// called out in the UI review at
// .review-screenshots/2026-09-30-ragabast-ui-review:
// the Documents table used to dump Tags as Go's default
// slice formatting (`[api http rest]`) which read as raw
// data, not as something the operator could scan.
//
// The Bulma-native treatment is `<span class="tag is-info">`.
// The contract tested here:
//
//   - every tag in the row's Tags slice renders as its own
//     <span class="tag ..."> element (no more "[...]" string
//     concatenation, no more literal brackets in the cell)
//   - the tags appear in the same order they arrived
//   - tags auto-escape through html/template (a tag with
//     a `<script>` payload renders as `&lt;script&gt;`, not
//     as raw HTML)
func TestDocumentsFallback_RendersTagsAsChips(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	t.Run("empty-state", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()

		assert.Contains(t, body, "No documents ingested yet.",
			"empty corpus must surface a friendly empty state")
		assert.NotContains(t, body, "[",
			"empty state must not leak Go's default slice formatting")
	})

	t.Run("tags-rendered-as-chips", func(t *testing.T) {
		require.NotNil(t, s.fallback.documents, "documents fallback template must be parsed")

		data := documentsFallbackData{
			Title: "Ingested Documents",
			Header: pageHeaderData{
				AuthEnabled: false,
				SignedIn:    false,
				ShowSignIn:  false,
				DisplayName: "",
				CsrfToken:   "",
				SignInURL:   "",
			},
			Total:        1,
			StartShowing: 1,
			EndShowing:   1,
			Limit:        25,
			PrevOffset:   -1,
			NextOffset:   -1,
			Documents: []documentsFallbackRow{
				{
					ID:           "doc-1",
					DisplayLabel: "Doc One",
					Tags:         []string{"alpha", "beta"},
					Category:     "Reference",
					Chunks:       3,
				},
			},
		}

		var buf strings.Builder
		require.NoError(t, s.fallback.documents.Execute(&buf, data))
		body := buf.String()

		// Each tag rendered as its own chip.
		assert.Contains(t, body, `<span class="tag is-info">alpha</span>`,
			"first tag must render as a Bulma chip")
		assert.Contains(t, body, `<span class="tag is-info">beta</span>`,
			"second tag must render as a Bulma chip")

		// Order preserved: alpha appears before beta.
		assert.Less(t, strings.Index(body, "alpha"), strings.Index(body, "beta"),
			"tag chips must render in input order")

		// The old "[alpha beta]" rendering is gone.
		assert.NotContains(t, body, "[alpha",
			"default slice formatting must not appear in the Tags cell")
	})

	t.Run("xss-tag-escaped", func(t *testing.T) {
		require.NotNil(t, s.fallback.documents)

		data := documentsFallbackData{
			Title: "Ingested Documents",
			Header: pageHeaderData{
				AuthEnabled: false, SignedIn: false, ShowSignIn: false,
			},
			Total:        1,
			StartShowing: 1, EndShowing: 1, Limit: 25,
			PrevOffset: -1, NextOffset: -1,
			Documents: []documentsFallbackRow{
				{
					ID:           "doc-evil",
					DisplayLabel: "Doc Evil",
					Tags:         []string{`<script>alert(1)</script>`},
					Category:     "",
					Chunks:       1,
				},
			},
		}

		var buf strings.Builder
		require.NoError(t, s.fallback.documents.Execute(&buf, data))
		body := buf.String()

		assert.NotContains(t, body, "<script>alert(1)</script>",
			"raw XSS in tag must not survive html/template escaping")
		assert.Contains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;",
			"XSS tag must appear in escaped form inside the chip")
	})
}

// TestDocumentsFallback_TableHeadersHaveScope pins the
// accessibility contract: every <th> in the documents table
// must declare scope="col" so screen-reader users get a
// usable column header association.
func TestDocumentsFallback_TableHeadersHaveScope(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	require.NotNil(t, s.fallback.documents, "documents fallback template must be parsed")

	data := documentsFallbackData{
		Title: "Ingested Documents",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
		},
		Total:        1,
		StartShowing: 1, EndShowing: 1, Limit: 25,
		PrevOffset: -1, NextOffset: -1,
		Documents: []documentsFallbackRow{
			{ID: "doc-1", DisplayLabel: "Doc One", Tags: []string{"a"}, Chunks: 1},
		},
	}
	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, data))
	body := buf.String()

	// Every column header must declare scope="col".
	for _, header := range []string{"Title", "ID", "Tags", "Category", "Chunks"} {
		assert.Contains(t, body, `<th scope="col">`+header+`</th>`,
			"every column header %q must declare scope=\"col\"", header)
	}
}
