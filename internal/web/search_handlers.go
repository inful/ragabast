package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// defaultSearchTopK is the form /search handler's default top_k. Larger
// than chat (10 vs 5) because search returns summaries the user skims;
// chat returns chunks the LLM consumes.
const defaultSearchTopK = 5

// maxSearchTopK caps the form /search handler's top_k. The HTML form's
// max="50" attribute is advisory only; this constant is the server-side
// security boundary. A curl request with top_k=1000000 lands here and is
// clamped before reaching the vector DB.
const maxSearchTopK = 50

// searchTopK reads an optional 'top_k' form value, falling back
// to defaultSearchTopK. The clamp at maxSearchTopK is the
// server-side security boundary (H-5); the HTML form's max="50"
// attribute is advisory only.
func searchTopK(r *http.Request) int {
	raw := strings.TrimSpace(r.FormValue("top_k"))
	if raw == "" {
		return defaultSearchTopK
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return defaultSearchTopK
	}
	if v > maxSearchTopK {
		return maxSearchTopK
	}
	return v
}

func (s *Server) handleSearchPage(w http.ResponseWriter, r *http.Request) {
	tags, categories, err := s.service.GetTagsAndCategories(r.Context())
	if err != nil {
		// Tag/category picker is best-effort UX. A failure here
		// shouldn't block the form from rendering — the user can
		// still type a query.
		tags = nil
		categories = nil
	}

	// Document ID autocomplete (issue #90). The list is small
	// in practice (operators with thousands of docs would already
	// have switched to programmatic ingest pipelines); the
	// ListDocumentsPaged call is bounded by the existing
	// pagination defaults so the page chrome doesn't grow
	// unbounded. A failure here is also best-effort UX — no
	// datalist means the input still works, just without
	// browser autocomplete.
	var documentIDs []string
	if docs, _, listErr := s.service.ListDocumentsPaged(r.Context(), 1000, 0); listErr == nil {
		for _, d := range docs {
			documentIDs = append(documentIDs, d.UID)
		}
	}

	s.renderTemplate(w, "search.html", map[string]any{
		"Title":       "Search",
		"Tags":        tags,
		"Categories":  categories,
		"DocumentIDs": documentIDs,
		"SourceKinds": knownSourceKinds(),
		// Search is stateless — no sticky default per source kind.
		// Pass an empty SelectedKinds; the template's "if len == 0
		// → check all" branch renders every box checked, the same
		// "all sources by default" first-visit UX as the chat form.
		"SelectedKinds": []string(nil),
		"CsrfToken":     CsrfTokenFromContext(r.Context()),
		"Header":        s.pageHeaderFromContext(r),
		// Phase 3.4 of plans/ux-overhaul.md: the result
		// count field is a <select> rather than an
		// <input type="number">. We pin the default
		// value here (5) so the first-render select
		// shows "5" as selected. A subsequent
		// submit-redirect cycle can carry a previous
		// value via this field.
		"TopK": 5,
	})
}

func (s *Server) handleSearchSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	query := r.FormValue("query")
	if query == "" {
		// Return 200 with a notification fragment rather than
		// 400 + plain text: htmx by default does not swap 4xx
		// responses, so a 400 here would leave the form looking
		// unchanged after a programmatic submit (curl, tests).
		// A 200 with a daisyUI alert gives htmx something
		// to swap into #search-results and gives the user a
		// visible reason nothing happened. The form's HTML
		// `required` attribute on the query input still blocks
		// empty submits client-side in real browsers.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `<div class="notification is-warning">Query is required.</div>`)
		return
	}

	limit := searchTopK(r)

	filters := service.SearchFilters{
		Tag:        r.FormValue("tag"),
		Category:   r.FormValue("category"),
		DocumentID: r.FormValue("document_id"),
		// Stage 2.6 — per-source-kind scoping on /search.
		// Each query is fresh; no sticky default. The handler
		// reads source_kinds from the form (multi-checkbox or
		// comma-separated curl) and passes it through to the
		// service. The post-filter (applySourceKindFilters)
		// drops out-of-scope results before they reach the
		// template. nil/empty → all sources (no filter).
		SourceKinds: parseSourceKindsForm(r),
	}

	results, err := s.service.Search(r.Context(), query, limit, filters)
	if err != nil {
		internalError(w, r, "search", err)
		return
	}

	// Pre-render each result's Content as sanitized HTML so
	// the template can drop it straight into the page instead
	// of dumping raw markdown. Without this, headings,
	// bold/italic, inline code, code fences, and lists all
	// show up as literal punctuation in the result cards.
	// Wraps as template.HTML because the markdown has already
	// been through bluemonday's UGCPolicy sanitizer below.
	rendered := make([]models.SearchResult, len(results))
	for i, r := range results {
		rendered[i] = r
		html, mdErr := renderChatMarkdownToSafeHTML(r.Content)
		if mdErr != nil {
			rendered[i].ContentHTML = template.HTML(template.HTMLEscapeString(r.Content))
		} else {
			rendered[i].ContentHTML = template.HTML(html)
		}
	}

	s.renderTemplate(w, "search_results.html", map[string]any{
		"Title":   "Search Results",
		"Query":   query,
		"Filters": filters,
		"Results": rendered,
	})
}
