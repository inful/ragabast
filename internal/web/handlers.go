package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

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
