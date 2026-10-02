package web

import (
	"net/http"
	"net/url"
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
		// Stage 2.5 — source-kind sticky default. The
		// unsafe serveBasicHTML path is the legacy fallback
		// (no embedded template); we still pass the multi-
		// select fields so the template's `{{ if .SourceKinds }}`
		// branch renders consistently. Empty slices fall
		// through to "no default" / "no kinds" — the help text
		// still explains the empty selection semantics.
		var selected []string
		if v, ok := data.(map[string]any); ok {
			if s, ok := v["SelectedKinds"].([]string); ok {
				selected = s
			}
		}
		s.renderFallback(w, "chat.html", chatFallbackData{
			Title:         titleFromMap(data, "RAGabast - Chat"),
			CsrfToken:     stringFromMap(data, "CsrfToken"),
			Header:        header,
			SourceKinds:   knownSourceKinds(),
			SelectedKinds: selected,
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
