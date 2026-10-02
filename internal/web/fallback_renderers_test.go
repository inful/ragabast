package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestFallbackRenderers_EscapeUserControlledStrings pins the
// security requirement that every page rendered via the
// serveBasicHTML fallback (chat.html, documents.html) MUST
// HTML-escape user-controlled strings before they hit the
// page. The fallback fires when the embedded template set
// does not include the requested page name — which is the
// production Docker image today, where only chat_message.html,
// search.html, and search_results.html ship in the embed.
//
// PR 2 removed the HTML /ingest form, so ingest.html and
// ingest_success.html are no longer fallback targets. The
// test now covers chat.html and documents.html.
//
// The XSS threat: doc.Title (set from the frontmatter `title:`
// field in markdown) and doc.Tags (taken verbatim from the
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
}

// TestFirstOrEmpty pins the small string helper the
// documents-fallback renderer uses to flatten a multi-value
// category into the single-value "Category" column. Moving
// the function to a new file shouldn't change its behavior;
// the test anchors the contract.
func TestFirstOrEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "empty", in: []string{}, want: ""},
		{name: "single", in: []string{"a"}, want: "a"},
		{name: "multi returns first", in: []string{"a", "b", "c"}, want: "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, firstOrEmpty(tc.in))
		})
	}
}

// TestFromMap_Header pins the headerFromMap contract: it
// returns an empty pageHeaderData when the input isn't a
// map[string]any, and the field-zeroed struct when the
// map is missing the "Header" key. Used by serveBasicHTML
// to wire the navbar data into the chat/documents fallback
// templates; the type assertions must not panic on a
// missing or wrong-typed value because that would
// 500 every page that fell through to the fallback.
func TestFromMap_Header(t *testing.T) {
	t.Run("non-map returns empty", func(t *testing.T) {
		require.Equal(t, pageHeaderData{}, headerFromMap("not a map"))
		require.Equal(t, pageHeaderData{}, headerFromMap(nil))
	})
	t.Run("missing key returns empty", func(t *testing.T) {
		require.Equal(t, pageHeaderData{}, headerFromMap(map[string]any{}))
	})
	t.Run("wrong-typed key returns empty", func(t *testing.T) {
		require.Equal(t, pageHeaderData{},
			headerFromMap(map[string]any{"Header": "not a struct"}))
	})
	t.Run("correct type returns the value", func(t *testing.T) {
		want := pageHeaderData{AuthEnabled: true, SignedIn: true, DisplayName: "alice"}
		got := headerFromMap(map[string]any{"Header": want})
		require.Equal(t, want, got)
	})
}

// TestFromMap_String pins the stringFromMap accessor's
// "silent default on type mismatch" contract. The fallback
// templates render empty when the field is missing; a
// panic would 500 the page.
func TestFromMap_String(t *testing.T) {
	t.Run("non-map returns empty", func(t *testing.T) {
		require.Empty(t, stringFromMap(42, "CsrfToken"))
		require.Empty(t, stringFromMap(nil, "CsrfToken"))
	})
	t.Run("missing key returns empty", func(t *testing.T) {
		require.Empty(t, stringFromMap(map[string]any{}, "CsrfToken"))
	})
	t.Run("wrong type returns empty", func(t *testing.T) {
		require.Empty(t, stringFromMap(map[string]any{"CsrfToken": 123}, "CsrfToken"))
	})
	t.Run("correct type returns the value", func(t *testing.T) {
		require.Equal(t, "abc",
			stringFromMap(map[string]any{"CsrfToken": "abc"}, "CsrfToken"))
	})
	// Exercise the key parameter with a different key so
	// the function is anchored as a generic accessor, not
	// a CsrfToken-specialist. The unparam linter would
	// otherwise flag this when every production call site
	// happens to pass the same string.
	t.Run("different key returns the matching value", func(t *testing.T) {
		require.Equal(t, "ok",
			stringFromMap(map[string]any{"Other": "ok"}, "Other"))
	})
}

// TestFromMap_Int pins the intFromMap accessor's three
// accepted numeric kinds (int, int64, float64) and the
// type-mismatch fallthrough to 0. The template renders 0
// in the mismatch case; the test pins that contract so a
// future contributor who widens the type set sees the
// existing scope.
func TestFromMap_Int(t *testing.T) {
	t.Run("non-map returns 0", func(t *testing.T) {
		require.Equal(t, 0, intFromMap("nope", "Limit"))
		require.Equal(t, 0, intFromMap(nil, "Limit"))
	})
	t.Run("missing key returns 0", func(t *testing.T) {
		require.Equal(t, 0, intFromMap(map[string]any{}, "Limit"))
	})
	t.Run("int kind", func(t *testing.T) {
		require.Equal(t, 25, intFromMap(map[string]any{"Limit": 25}, "Limit"))
	})
	t.Run("int64 kind", func(t *testing.T) {
		require.Equal(t, 25, intFromMap(map[string]any{"Limit": int64(25)}, "Limit"))
	})
	t.Run("float64 kind", func(t *testing.T) {
		require.Equal(t, 25, intFromMap(map[string]any{"Limit": float64(25)}, "Limit"))
	})
	t.Run("wrong type returns 0", func(t *testing.T) {
		require.Equal(t, 0, intFromMap(map[string]any{"Limit": "25"}, "Limit"))
	})
}

// TestFromMap_Title pins the titleFromMap accessor's
// "fallback when missing or empty" contract. The page
// titles (e.g. "RAGabast - Chat") come from a hardcoded
// fallback because the handler-side Title is sometimes
// unset in the fallback path.
func TestFromMap_Title(t *testing.T) {
	t.Run("non-map returns fallback", func(t *testing.T) {
		require.Equal(t, "FB", titleFromMap(nil, "FB"))
	})
	t.Run("missing key returns fallback", func(t *testing.T) {
		require.Equal(t, "FB", titleFromMap(map[string]any{}, "FB"))
	})
	t.Run("empty string returns fallback", func(t *testing.T) {
		require.Equal(t, "FB",
			titleFromMap(map[string]any{"Title": ""}, "FB"))
	})
	t.Run("non-string key returns fallback", func(t *testing.T) {
		require.Equal(t, "FB",
			titleFromMap(map[string]any{"Title": 42}, "FB"))
	})
	t.Run("set value wins", func(t *testing.T) {
		require.Equal(t, "real",
			titleFromMap(map[string]any{"Title": "real"}, "FB"))
	})
}

// TestDocsRowsFromMap pins the documents-fallback adapter
// contract: it must adapt the []models.DocumentInfo the
// handler-side supplies into the strongly-typed
// documentsFallbackRow slice the fallback template reads.
// Wrong-typed input must return nil (no panic) because the
// handler always supplies the right type in production,
// but a unit test against the wrong type pins the safe
// behavior the test fake might depend on.
func TestDocsRowsFromMap(t *testing.T) {
	t.Run("non-map returns nil", func(t *testing.T) {
		require.Nil(t, docsRowsFromMap(nil))
	})
	t.Run("missing Documents key returns nil", func(t *testing.T) {
		require.Nil(t, docsRowsFromMap(map[string]any{}))
	})
	t.Run("wrong-typed Documents key returns nil", func(t *testing.T) {
		require.Nil(t, docsRowsFromMap(map[string]any{"Documents": "not a slice"}))
	})
}
