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

// defaultChatTopK is the number of retrieved chunks the chat form uses when
// the client does not pass an explicit top_k. Each chunk is augmented with
// its parent section header, so 5 results is a comfortable default.
const defaultChatTopK = 5

// maxChatTopK caps the chat form's top_k to prevent a runaway client from
// pulling the entire vector DB into the LLM context.
const maxChatTopK = 50

// defaultSearchTopK is the form /search handler's default top_k. Larger
// than chat (10 vs 5) because search returns summaries the user skims;
// chat returns chunks the LLM consumes.
const defaultSearchTopK = 5

// maxSearchTopK caps the form /search handler's top_k. The HTML form's
// max="50" attribute is advisory only; this constant is the server-side
// security boundary. A curl request with top_k=1000000 lands here and is
// clamped before reaching the vector DB.
const maxSearchTopK = 50

// chatTopK reads an optional 'top_k' form value, falling back to
// defaultChatTopK. Out-of-range or unparseable values are clamped.
func chatTopK(r *http.Request) int {
	raw := strings.TrimSpace(r.FormValue("top_k"))
	if raw == "" {
		return defaultChatTopK
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return defaultChatTopK
	}
	if v > maxChatTopK {
		return maxChatTopK
	}
	return v
}

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
// All four pages that DO fall back here (chat.html, ingest.html,
// ingest_success.html, documents.html) are rendered through
// html/template instances pre-parsed in newFallbackTemplates
// (see fallback_renderers.go). That guarantees every {{ }}
// substitution is contextually escaped — fixing the previous
// stored-XSS bug where fmt.Fprintf %s/%v interpolated
// attacker-controlled strings (doc.Title from the H1 header,
// doc.Tags from YAML frontmatter) directly into the response.
func (s *Server) serveBasicHTML(w http.ResponseWriter, templateName string, data any) {
	switch templateName {
	case "chat.html":
		s.renderFallback(w, "chat.html", chatFallbackData{
			Title:     titleFromMap(data, "RAGabast - Chat"),
			CsrfToken: stringFromMap(data, "CsrfToken"),
		})
	case "ingest.html":
		s.renderFallback(w, "ingest.html", ingestFallbackData{
			Title:     titleFromMap(data, "RAGabast - Ingest"),
			CsrfToken: stringFromMap(data, "CsrfToken"),
		})
	case "ingest_success.html":
		s.renderFallback(w, "ingest_success.html", ingestSuccessFallbackData{
			Title:      titleFromMap(data, "Ingest Successful"),
			DocumentID: stringFromMap(data, "DocumentID"),
			Chunks:     intFromMap(data, "Chunks"),
			Tags:       tagsFromMap(data),
		})
	case "documents.html":
		s.renderFallback(w, "documents.html", documentsFallbackData{
			Title:        titleFromMap(data, "RAGabast - Documents"),
			Documents:    docsRowsFromMap(data),
			Total:        intFromMap(data, "Total"),
			Limit:        intFromMap(data, "Limit"),
			Offset:       intFromMap(data, "Offset"),
			PrevOffset:   intFromMap(data, "PrevOffset"),
			NextOffset:   intFromMap(data, "NextOffset"),
			StartShowing: intFromMap(data, "StartShowing"),
			EndShowing:   intFromMap(data, "EndShowing"),
		})
	default:
		http.NotFound(w, nil)
	}
}

// titleFromMap extracts a string "Title" field from the
// map[string]any shape that renderTemplate passes through. Falls
// back to the supplied default so a missing field does not
// render as "<no value>".
func titleFromMap(data any, fallback string) string {
	m, ok := data.(map[string]any)
	if !ok {
		return fallback
	}
	if t, ok := m["Title"].(string); ok && t != "" {
		return t
	}
	return fallback
}

// stringFromMap is the generic-string accessor used by fields
// whose absence should silently render as empty rather than panic.
func stringFromMap(data any, key string) string {
	m, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// intFromMap is the int accessor. Returns 0 on type mismatch;
// the {{ .Chunks }} substitution prints 0 in that case which
// matches the existing fmt.Fprintf behavior.
func intFromMap(data any, key string) int {
	m, ok := data.(map[string]any)
	if !ok {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// tagsFromMap extracts a []string from the data map, regardless
// of whether the handler stored it as []string or []any. The
// legacy handlers stored []any because the data map flowed from
// an untyped literal.
func tagsFromMap(data any) []string {
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	switch v := m["Tags"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// docsRowsFromMap adapts the []models.DocumentInfo slice that
// handleDocumentsPage passes through into the strongly-typed
// documentsFallbackRow slice that the fallback template expects.
// Every field that flows into the page is HTML-escaped by
// html/template at render time; this helper only changes types.
func docsRowsFromMap(data any) []documentsFallbackRow {
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := m["Documents"].([]models.DocumentInfo)
	if !ok {
		return nil
	}
	out := make([]documentsFallbackRow, 0, len(raw))
	for _, item := range raw {
		out = append(out, documentsFallbackRow{
			Title:    item.Title,
			ID:       item.ID,
			Tags:     item.Tags,
			Category: firstOrEmpty(item.Categories),
			Chunks:   item.ChunkCount,
		})
	}
	return out
}

// firstOrEmpty returns the first element of a slice or "" if the
// slice is empty. Used for the Category column which historically
// showed only the first category in the table.
func firstOrEmpty(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
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
	s.renderTemplate(w, "search.html", map[string]any{
		"Title":      "Search",
		"Tags":       tags,
		"Categories": categories,
		"CsrfToken":  CsrfTokenFromContext(r.Context()),
	})
}

func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, "chat.html", map[string]any{
		"Title":     "RAGabast - Chat",
		"CsrfToken": CsrfTokenFromContext(r.Context()),
	})
}

func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	msg := strings.TrimSpace(r.FormValue("message"))
	if msg == "" {
		http.Error(w, "Message is required", http.StatusBadRequest)
		return
	}

	answer, info, err := s.service.QueryDebugWithOptions(r.Context(), msg, chatTopK(r), service.LLMOptions{})
	if err != nil {
		internalError(w, r, "query", err)
		return
	}

	var sources []models.SearchResult
	if info != nil {
		sources = info.Results
	}

	// Strip the model's own native thinking tokens
	// (<think>...</think> and the Qwen-style [think]...[/think]
	// variant). Some model families (Qwen, DeepSeek, ...) emit
	// their own reasoning tokens as a parallel channel to the
	// ragabast-directed <scratchpad>...</scratchpad> format.
	// Without this stripper, the user's visible reply includes
	// the model's "thinking out loud" preamble even when the
	// model also complies with our scratchpad directive. This is
	// the most common source of leaked preamble that the user
	// has been seeing.
	answer = service.StripThinkTags(answer)

	// Strip the <scratchpad>...</scratchpad> block the prompt
	// asks the LLM to wrap its reasoning in. Deterministic — no
	// heuristic matching of "Let me check…" style starters. If
	// the model forgot the format, this is a no-op and the next
	// step (StripLeadingThinking) acts as the heuristic backstop.
	answer = service.StripScratchpad(answer)

	// Heuristic backstop for models that didn't follow the
	// scratchpad format: strip leading "Let me check…" /
	// "Wait, actually…" / "I need to look at…" / "Hmm, that's
	// interesting…" paragraphs that some models emit BEFORE the
	// actual answer. Stops as soon as it finds a paragraph whose
	// first line doesn't look like thinking, so a real answer
	// that happens to begin with one of these markers is
	// preserved.
	answer = service.StripLeadingThinking(answer)

	// Strip a trailing "Links:" section the model emits despite
	// the prompt telling it not to. Inline [src:N] citations
	// cover the legitimate cases; the trailing section often
	// contains placeholder URLs ("https://docs.example.com")
	// that the model invented.
	answer = service.StripTrailingLinksSection(answer)

	// Inline [src:N] markers → markdown links to source N's URL.
	// Must run BEFORE the markdown renderer so the resulting
	// [title](url) syntax gets converted to <a> tags by the
	// markdown pipeline. The function is a no-op on replies that
	// don't use the marker syntax (common for replies that don't
	// cite anything).
	answerWithInlineLinks := service.InlineSourceLinks(answer, sources)

	// Render the (now inline-linked) LLM reply to HTML.
	//
	// Trust model: the LLM reply is untrusted text. Anything
	// that lands in {{ .AnswerHTML }} on the page is wrapped
	// as template.HTML (so html/template does not double-escape
	// the legitimate markdown tags we just rendered). Wrapping
	// as template.HTML is a security smell — every other field
	// in the template goes through html/template's auto-escape,
	// so this one field becomes "trusted by convention". To
	// keep that trust explicit, the pipeline below ends with a
	// second bluemonday UGCPolicy pass (sanitizeForChatHTML)
	// that strips anything the markdown renderer or
	// InlineSourceLinks introduced that the first sanitizer
	// pass missed. The markdown renderer already runs bluemonday
	// once (see internal/web/markdown.go); this second pass is
	// defense in depth, not a substitute.
	answerHTML, mdErr := renderChatMarkdownToSafeHTML(answerWithInlineLinks)
	if mdErr != nil {
		// Fall back to escaped plaintext so the user still sees
		// the assistant's reply if markdown rendering blows up.
		// The string is HTML-escaped so it is safe to wrap as
		// template.HTML below.
		answerHTML = template.HTMLEscapeString(answerWithInlineLinks)
	}
	answerHTML = sanitizeForChatHTML(answerHTML)

	s.renderTemplate(w, "chat_message.html", map[string]any{
		"User":       msg,
		"Answer":     answerWithInlineLinks,
		"AnswerHTML": template.HTML(answerHTML),
		"Sources":    sources,
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
		_, _ = fmt.Fprint(w, `<div class="notification is-warning is-light">Query is required.</div>`)
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

func (s *Server) handleIngestPage(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, "ingest.html", map[string]any{
		"Title":     "RAGabast - Ingest",
		"CsrfToken": CsrfTokenFromContext(r.Context()),
	})
}

func (s *Server) handleIngestSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	content := r.FormValue("content")
	if content == "" {
		http.Error(w, "Content is required", http.StatusBadRequest)
		return
	}

	// Per-document size cap (server.max_ingest_document_bytes).
	// The 10 MiB request-body limit (H-2) caps the whole
	// request, but a single ingest request is one document —
	// the smaller per-document cap protects the chunker and
	// embedding model from being pinned by one giant input.
	if maxBytes := s.config.Server.MaxIngestDocumentBytes; maxBytes > 0 && len(content) > maxBytes {
		http.Error(w, "Document exceeds server.max_ingest_document_bytes", http.StatusRequestEntityTooLarge)
		return
	}

	// Ingest document
	doc, err := s.service.IngestDocument(r.Context(), content)
	if err != nil {
		internalError(w, r, "ingest", err)
		return
	}

	s.renderTemplate(w, "ingest_success.html", map[string]any{
		"Title":      "Ingest Successful",
		"DocumentID": doc.ID,
		"Chunks":     len(doc.Chunks),
		"Tags":       doc.Tags,
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
		"Documents":    docs,
		"Total":        total,
		"Limit":        limit,
		"Offset":       offset,
		"PrevOffset":   prevOffset,
		"NextOffset":   nextOffset,
		"StartShowing": startShowing,
		"EndShowing":   endShowing,
	})
}

func (s *Server) handleStatic(w http.ResponseWriter, _ *http.Request) {
	// For now, serve a basic response
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("Static assets would be served here"))
}
