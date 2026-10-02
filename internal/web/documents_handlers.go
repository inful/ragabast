package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

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
