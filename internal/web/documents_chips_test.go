package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

		assert.Contains(t, body, "Nothing here yet.",
			"empty corpus must surface the Phase 6.1 copy (Nothing here yet.)")
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

		// Each tag rendered as its own chip. Phase 2e
		// of the Bulma -> DaisyUI migration (see
		// plans/daisyui-migration.md) swapped Bulma's
		// `tag is-info` for daisyUI's `badge badge-info`.
		// Phase 4.3 of plans/ux-overhaul.md upgrades
		// the static <span> to a clickable <a> linking
		// to /search?tag=foo. We use a regex (rather
		// than a literal substring) to tolerate the
		// `hover:badge-primary` modifier Phase 4.3
		// adds on top of the base badge classes.
		assert.Regexp(t, `<a[^>]*class="[^"]*\bbadge-info\b[^"]*"[^>]*>alpha</a>`, body,
			"first tag must render as a daisyUI badge anchor")
		assert.Regexp(t, `<a[^>]*class="[^"]*\bbadge-info\b[^"]*"[^>]*>beta</a>`, body,
			"second tag must render as a daisyUI badge anchor")

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
	for _, header := range []string{"Title", "ID", "Tags", "Category", "Chunks", "Ingested"} {
		assert.Contains(t, body, `<th scope="col">`+header+`</th>`,
			"every column header %q must declare scope=\"col\"", header)
	}
}

// TestDocumentsFallback_RendersIngestedColumn pins the
// UX fix called out in the design review: the Documents
// table previously carried Title / ID / Tags / Category /
// Chunks, with no signal for *when* a document was added.
// For a RAG tool whose value proposition is "what's in the
// corpus right now," that absence is the loudest missing
// signal on the page. The fix: an Ingested column that
// renders the row's IngestedAt time.
//
// Contract:
//
//   - the Ingested header renders in <thead> with scope="col"
//   - each row renders an Ingested cell with a formatted
//     timestamp (so the operator can see "2 hours ago" /
//     "3 days ago" without a JS date library)
//   - the IngestedAt field on documentsFallbackRow flows
//     through html/template auto-escape (a row whose
//     IngestedAt is the zero value renders as a fallback
//     placeholder, not an unparseable string)
func TestDocumentsFallback_RendersIngestedColumn(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	require.NotNil(t, s.fallback.documents, "documents fallback template must be parsed")

	now := time.Date(2026, 9, 30, 14, 0, 0, 0, time.UTC)
	data := documentsFallbackData{
		Title: "Ingested Documents",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
		},
		Total:        1,
		StartShowing: 1, EndShowing: 1, Limit: 25,
		PrevOffset: -1, NextOffset: -1,
		Documents: []documentsFallbackRow{
			{ID: "doc-1", DisplayLabel: "Doc One", Tags: []string{"a"}, Chunks: 1, IngestedAt: now},
		},
	}
	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, data))
	body := buf.String()

	// Header must declare the column.
	assert.Contains(t, body, `<th scope="col">Ingested</th>`,
		"the Ingested column must be declared in the table header with scope=\"col\"")

	// Row must render a recognizable timestamp. We don't
	// pin the exact format string (the Go time format
	// constant is a presentation decision the designer can
	// iterate on) — only that the year appears, so a row
	// that rendered a placeholder instead would fail.
	assert.Contains(t, body, "2026",
		"the Ingested cell must render a year-bearing timestamp; the zero-value fallback for unset IngestedAt is something else")
}

// TestDocumentsFallback_MissingIngestedRendersFallback pins
// the contract for legacy documents (ingested before
// IngestedAt was tracked): the cell renders a quiet
// placeholder, not the literal Go zero-value timestamp
// ("0001-01-01") which would look like a bug to the
// operator.
func TestDocumentsFallback_MissingIngestedRendersFallback(t *testing.T) {
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
			// IngestedAt is the zero value — the row was
			// ingested before the field existed, or the
			// upstream data path didn't set it.
			{ID: "legacy", DisplayLabel: "Legacy Doc", Tags: []string{"a"}, Chunks: 1},
		},
	}
	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, data))
	body := buf.String()

	assert.NotContains(t, body, "0001",
		"the Ingested cell must not render Go's zero-value timestamp literal \"0001-01-01\" — show a placeholder instead")
}

// TestDocumentsFallback_UsesDaisyUIComponents pins the
// post-migration shape of the /documents page (Phase 2e of
// the Bulma -> DaisyUI migration; see
// plans/daisyui-migration.md). The page moves from
// Bulma's component classes to daisyUI's idiomatic
// equivalents: the tag chips become badges, the table
// becomes daisyUI's table-zebra, the delete button
// becomes btn btn-error, the empty-state becomes alert.
//
// The page also gains a <link> to daisyui.min.css so the
// new class names are actually styled. Bulma stays
// loaded until Phase 3 (the navbar partial still uses
// Bulma).
func TestDocumentsFallback_UsesDaisyUIComponents(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	require.NotNil(t, s.fallback.documents, "documents fallback template must be parsed")

	data := documentsFallbackData{
		Title: "Ingested Documents",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
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
				Tags:         []string{"alpha"},
				Category:     "Reference",
				Chunks:       3,
			},
		},
	}

	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, data))
	body := buf.String()

	// daisyUI components used in the page.
	require.Contains(t, body, `daisyui.min.css`,
		"the documents page must load daisyui.min.css so the new daisyUI class names are actually styled (Phase 2e)")

	// The table uses daisyUI's `table table-zebra w-full`
	// (was Bulma's `table is-fullwidth is-striped`).
	require.Regexp(t, `<table[^>]*\bclass="[^"]*\btable-zebra\b`, body,
		"the documents table must use daisyUI's `table-zebra` class")
	require.Regexp(t, `<table[^>]*\bclass="[^"]*\bw-full\b`, body,
		"the documents table must use daisyUI's `w-full` utility")

	// The delete button is a daisyUI btn that
	// opens the confirmation modal (Phase 4.5 of
	// plans/ux-overhaul.md replaces the inline
	// checkbox + submit pattern). The trigger
	// button uses btn-ghost + text-error (a quiet
	// affordance that brightens to the danger
	// color via the text-error utility). The
	// actual destructive button is inside the
	// modal; TestDocuments_DeleteUsesModal (new
	// in documents_overhaul_test.go) pins the
	// modal rendering.
	require.Regexp(t, `<button[^>]*class="[^"]*\bbtn\b[^"]*"[^>]*data-modal-open=`, body,
		"the row's delete action must be a <button> that opens a confirmation modal (Phase 4.5)")

	// The legacy <input type="checkbox" name="confirm">
	// pattern is gone. We don't pin 'no checkbox
	// exists' (a future filter-bar checkbox is
	// fine); we pin 'no checkbox carries the name
	// "confirm"'.
	assert.NotRegexp(t, `<input[^>]*type="checkbox"[^>]*name="confirm"`, body,
		"the legacy <input type=\"checkbox\" name=\"confirm\"> delete confirmation must be gone (Phase 4.5 replaces with modal)")

	// Anti-regression: no Bulma class names remain on
	// the page (the navbar still uses Bulma but is
	// covered by the header partial, not the
	// documentsFallbackBody).
	require.NotRegexp(t, `class="table is-fullwidth is-striped"`, body,
		"the documents page must not use Bulma's `table is-fullwidth is-striped` (daisyUI's `table table-zebra w-full` is the equivalent)")
	require.NotRegexp(t, `class="button is-small is-danger"`, body,
		"the delete button must not use Bulma's `button is-small is-danger` (daisyUI's `btn btn-sm btn-error` is the equivalent)")
	require.NotRegexp(t, `class="checkbox is-small"`, body,
		"the confirm checkbox must not use Bulma's `checkbox is-small` (daisyUI's `checkbox checkbox-sm` is the equivalent)")
}
