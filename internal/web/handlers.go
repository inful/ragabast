package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
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

// renderTemplate renders a template with the given data.
func (s *Server) renderTemplate(w http.ResponseWriter, templateName string, data any) {
	// If templates are not loaded, serve basic HTML
	if s.templates == nil || len(s.templates.Templates()) == 0 {
		s.serveBasicHTML(w, templateName, data)
		return
	}

	tmpl := s.templates.Lookup(templateName)
	if tmpl == nil {
		// If the template doesn't exist (e.g. templates dir is empty), fall back to the
		// built-in HTML responses.
		s.serveBasicHTML(w, templateName, data)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		internalError(w, nil, "template render", err)
	}
}

// serveBasicHTML serves basic HTML when templates are not available.
// search.html, search_results.html, and chat_message.html are
// intentionally NOT served here: the in-tree templates/search.html,
// templates/search_results.html, and templates/chat_message.html
// are the canonical forms. Earlier Go-string fallbacks for
// search had a `[]models.SearchResult` type assertion that panicked
// on every search request; routing them back through a fallback
// would just bring the panic back. chat_message.html also surfaced
// no source documents at all (handleChatMessage discarded
// QueryDebugInfo.Results), so we want the in-tree template there
// too. These three are absent here on purpose.
//
// All three pages that DO fall back here (chat.html,
// documents.html, and the search fallback if one is added
// later) are rendered through html/template instances
// pre-parsed in newFallbackTemplates (see fallback_renderers.go).
// That guarantees every {{ }} substitution is contextually
// escaped — fixing the previous stored-XSS bug where
// fmt.Fprintf %s/%v interpolated attacker-controlled strings
// (doc.Title from the frontmatter `title:` field, doc.Tags
// from YAML frontmatter) directly into the response.
func (s *Server) serveBasicHTML(w http.ResponseWriter, templateName string, data any) {
	header := headerFromMap(data)
	switch templateName {
	case "chat.html":
		s.renderFallback(w, "chat.html", chatFallbackData{
			Title:     titleFromMap(data, "RAGabast - Chat"),
			CsrfToken: stringFromMap(data, "CsrfToken"),
			Header:    header,
		})
	case "documents.html":
		s.renderFallback(w, "documents.html", documentsFallbackData{
			Title:        titleFromMap(data, "RAGabast - Documents"),
			CsrfToken:    stringFromMap(data, "CsrfToken"),
			Documents:    docsRowsFromMap(data),
			Total:        intFromMap(data, "Total"),
			Limit:        intFromMap(data, "Limit"),
			Offset:       intFromMap(data, "Offset"),
			PrevOffset:   intFromMap(data, "PrevOffset"),
			NextOffset:   intFromMap(data, "NextOffset"),
			StartShowing: intFromMap(data, "StartShowing"),
			EndShowing:   intFromMap(data, "EndShowing"),
			Header:       header,
		})
	default:
		http.NotFound(w, nil)
	}
}

// pageHeaderFromContext builds the per-request pageHeaderData
// the navbar template consumes. Every page handler calls this
// (typically via s.pageHeaderFromContext) so the header
// stays consistent across chat / ingest / documents / etc.
//
// Decision matrix:
//
//	                                 AuthEnabled=true
//	                            +-------------------+
//	                            | SignedIn          |
//	                            | true   | false     |
//	+--------+-----------------+--------+----------+
//	|        | SignedIn=true   | render |  —       |
//	| Auth   |                 | header |          |
//	| Ena... |                 | with   |          |
//	| bled   |                 | user + |          |
//	| =true  |                 | logout |          |
//	|        +-----------------+--------+----------+
//	|        | SignedIn=false  |  —     | "Sign    |
//	|        |                 |        | in"     |
//	|        |                 |        | link     |
//	+--------+-----------------+--------+----------+
//	| AuthEnabled=false            | render nothing |
//	+-------------------------------+----------------+
//
// The "next=" parameter threads the current URL into the
// Sign in link so the chooser page can redirect the user
// back to where they were going after auth completes.
func (s *Server) pageHeaderFromContext(r *http.Request) pageHeaderData {
	authEnabled := s.oauthHandlers != nil && len(s.oauthHandlers.providers) > 0
	if !authEnabled {
		return pageHeaderData{}
	}

	h := pageHeaderData{
		AuthEnabled: true,
		CsrfToken:   CsrfTokenFromContext(r.Context()),
	}

	if u := UserFromContext(r.Context()); u.Subject != "" {
		h.SignedIn = true
		h.DisplayName = u.DisplayLabel()

		return h
	}

	h.ShowSignIn = true
	h.SignInURL = "/auth/login?next=" + url.QueryEscape(safeNextPath(r.URL.Path))

	return h
}

// safeNextPath returns r.URL.Path with a leading slash; if
// the URL has no path (rare but possible), it returns "/"
// so the chooser's ?next= is never empty.
func safeNextPath(p string) string {
	if p == "" {
		return "/"
	}

	return p
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
		"CsrfToken":   CsrfTokenFromContext(r.Context()),
		"Header":      s.pageHeaderFromContext(r),
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
		// A 200 with a Bulma notification gives htmx something
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

func (s *Server) handleDocumentsPage(w http.ResponseWriter, r *http.Request) {
	// Parse pagination from query string (?limit=&offset=).
	// Same clamping rules as the JSON endpoint: default
	// limit 25, cap 1000, offset clamped to [0, total].
	limit := 25
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	docs, total, err := s.service.ListDocumentsPaged(r.Context(), limit, offset)
	if err != nil {
		internalError(w, r, "list documents", err)
		return
	}

	// Pre-compute pagination helpers so the template
	// stays free of arithmetic funcs (html/template
	// doesn't ship add/sub — pulling in sprig just for
	// this is overkill). -1 sentinel means "no
	// previous/next link" — the template suppresses the
	// link for that case.
	prevOffset := offset - limit
	if prevOffset < 0 {
		prevOffset = -1
	}
	nextOffset := offset + limit
	if nextOffset >= total {
		nextOffset = -1
	}
	startShowing := min(offset+1, total)
	endShowing := min(offset+len(docs), total)

	s.renderTemplate(w, "documents.html", map[string]any{
		"Title":        "RAGabast - Documents",
		"CsrfToken":    CsrfTokenFromContext(r.Context()),
		"Documents":    docs,
		"Total":        total,
		"Limit":        limit,
		"Offset":       offset,
		"PrevOffset":   prevOffset,
		"NextOffset":   nextOffset,
		"StartShowing": startShowing,
		"EndShowing":   endShowing,
		"Header":       s.pageHeaderFromContext(r),
	})
}

// handleDocumentDelete is the form-mounted delete endpoint for
// the /documents UI. Three layers of guard against accidental or
// forged deletes:
//
//   - chi path param {document_id} must be non-empty (empty is
//     a 400, not a 404 from the mux — explicit operator-facing
//     failure)
//   - the form must carry confirm=1, a required checkbox the
//     /documents template emits. Without it the browser-side
//     required attribute blocks the submit; a forged POST that
//     omits it gets a 400 here
//   - csrf middleware (already on the route) requires a matching
//     csrf_token form value; mismatches get a 403
//
// On success the handler calls svc.DeleteDocument and 302s to
// /documents so the operator lands back on the list with the
// row gone. Errors during delete (e.g. concurrent re-ingest)
// bubble through internalError as a 500.
func (s *Server) handleDocumentDelete(w http.ResponseWriter, r *http.Request) {
	documentID := strings.TrimSpace(chi.URLParam(r, "document_id"))
	if documentID == "" {
		http.Error(w, "document_id is required", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}
	if r.FormValue("confirm") != "1" {
		http.Error(w, "confirm=1 is required to delete a document", http.StatusBadRequest)
		return
	}

	if err := s.service.DeleteDocument(r.Context(), documentID); err != nil {
		internalError(w, r, "delete document", err)
		return
	}

	http.Redirect(w, r, "/documents", http.StatusFound)
}
