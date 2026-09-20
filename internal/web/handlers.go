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
func (s *Server) serveBasicHTML(w http.ResponseWriter, templateName string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Simple HTML templates
	switch templateName {
	case "chat.html":
		s.serveChatHTML(w, data)
	case "ingest.html":
		s.serveIngestHTML(w, data)
	case "ingest_success.html":
		s.serveIngestSuccessHTML(w, data)
	case "documents.html":
		s.serveDocumentsHTML(w, data)
	default:
		http.NotFound(w, nil)
	}
}

func (s *Server) serveChatHTML(w http.ResponseWriter, data any) {
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
	<title>%s</title>
	<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
	<script src="https://unpkg.com/htmx.org@1.9.10"></script>
	<style>
		.chat-log { max-height: 60vh; overflow-y: auto; }
		.chat-msg { max-width: 100%%; overflow-wrap: anywhere; word-break: break-word; }
		pre.chat-msg { white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-word; overflow-x: hidden; }
		.chat-msg a { overflow-wrap: anywhere; word-break: break-word; }
		.chat-msg pre, .chat-msg code { white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-word; }
		.htmx-indicator { display: none; }
		.htmx-request.htmx-indicator { display: inline-block; }
	</style>
</head>
<body class="container mt-4">
	<h1 class="title">Chat</h1>
	<p class="subtitle">Ask questions against the ingested documents.</p>

	<div id="chat-messages" class="box chat-log">
		<div class="content" id="chat-messages-placeholder">
			<p class="has-text-grey">No messages yet.</p>
		</div>
	</div>

	<form id="chat-form" class="box" hx-post="/chat/message" hx-target="#chat-messages" hx-swap="beforeend" hx-indicator="#chat-indicator" hx-disabled-elt="#chat-send, #chat-input" hx-on::before-request="document.getElementById('chat-send')?.classList.add('is-loading')" hx-on::after-request="this.reset(); document.getElementById('chat-send')?.classList.remove('is-loading'); document.getElementById('chat-input')?.focus()" hx-on::response-error="document.getElementById('chat-send')?.classList.remove('is-loading')">
		<div class="field">
			<label class="label">Message</label>
			<div class="control">
				<textarea id="chat-input" class="textarea" name="message" rows="2" placeholder="Ask a question..." required></textarea>
			</div>
		</div>
		<div class="field is-grouped">
			<div class="control">
				<button id="chat-send" class="button is-warning" type="submit">Send</button>
			</div>
			<div class="control htmx-indicator" id="chat-indicator">
				<span class="tag is-light">Thinking…</span>
			</div>
		</div>
	</form>

	<script>
		(function () {
			function scrollChatToBottom() {
				var el = document.getElementById('chat-messages');
				if (!el) return;
				el.scrollTop = el.scrollHeight;
			}

			function submitChatForm() {
				var form = document.getElementById('chat-form');
				if (!form) return;
				if (typeof form.requestSubmit === 'function') {
					form.requestSubmit();
					return;
				}
				form.submit();
			}

			var input = document.getElementById('chat-input');
			if (input) {
				input.addEventListener('keydown', function (e) {
					// Cmd+Enter (macOS) or Ctrl+Enter (Windows/Linux) submits.
					if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
						e.preventDefault();
						submitChatForm();
					}
					// Esc clears the input.
					if (e.key === 'Escape') {
						e.preventDefault();
						input.value = '';
						input.focus();
					}
				});
			}

			document.body.addEventListener('htmx:afterSwap', function (evt) {
				if (evt.target && evt.target.id === 'chat-messages') {
					scrollChatToBottom();
				}
			});

			// If the page loads with existing content (future), keep it pinned.
			scrollChatToBottom();
		})();
	</script>
</body>
</html>`, data.(map[string]any)["Title"])
}

func (s *Server) serveIngestHTML(w http.ResponseWriter, data any) {
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>%s</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
</head>
<body class="container mt-4">
    <h1 class="title">Ingest Document</h1>
    <form method="post" action="/ingest">
        <div class="field">
            <label class="label">Docbuilder Content</label>
            <div class="control">
                <textarea class="textarea" name="content" rows="15" placeholder="Paste your docbuilder markdown content here..." required></textarea>
            </div>
            <p class="help">Include YAML frontmatter with fingerprint, uid, tags, categories, and URLs</p>
        </div>
        <div class="field">
            <div class="control">
                <button class="button is-primary" type="submit">Ingest</button>
                <a href="/" class="button is-light">Back</a>
            </div>
        </div>
    </form>
</body>
</html>`, data.(map[string]any)["Title"])
}

func (s *Server) serveIngestSuccessHTML(w http.ResponseWriter, data any) {
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>%s</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
</head>
<body class="container mt-4">
    <div class="notification is-success">
        <h1 class="title">Document Ingested Successfully!</h1>
        <p><strong>Document ID:</strong> %s</p>
        <p><strong>Chunks Created:</strong> %d</p>
        <p><strong>Tags:</strong> %v</p>
    </div>
    <div class="buttons">
        <a href="/ingest" class="button is-primary">Ingest Another</a>
        <a href="/search" class="button is-info">Search</a>
        <a href="/" class="button is-light">Home</a>
    </div>
</body>
</html>`, data.(map[string]any)["Title"],
		data.(map[string]any)["DocumentID"],
		data.(map[string]any)["Chunks"],
		data.(map[string]any)["Tags"])
}

func (s *Server) serveDocumentsHTML(w http.ResponseWriter, data any) {
	docs := data.(map[string]any)["Documents"]
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>%s</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
</head>
<body class="container mt-4">
    <h1 class="title">Ingested Documents</h1>
    <a href="/" class="button is-light mb-4">Back</a>
`, data.(map[string]any)["Title"])

	if docs != nil && len(docs.([]any)) > 0 {
		_, _ = fmt.Fprintf(w, `<table class="table is-fullwidth is-striped">
            <thead><tr><th>Title</th><th>ID</th><th>Tags</th><th>Category</th><th>Chunks</th></tr></thead>
            <tbody>`)
		for _, d := range docs.([]any) {
			doc := d.(map[string]any)
			_, _ = fmt.Fprintf(w, `<tr>
                <td>%s</td>
                <td><code>%s</code></td>
                <td>%v</td>
                <td>%s</td>
                <td>%d</td>
            </tr>`, doc["Title"], doc["ID"], doc["Tags"], doc["Category"], doc["Chunks"])
		}
		_, _ = fmt.Fprintf(w, `</tbody></table>`)
	} else {
		_, _ = fmt.Fprintf(w, `<p>No documents ingested yet.</p>`)
	}

	_, _ = fmt.Fprintf(w, `</body></html>`)
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
	})
}

func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, "chat.html", map[string]any{
		"Title": "RAGabast - Chat",
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

	// Render the (now inline-linked) LLM reply to safe HTML.
	// Markdown-to-HTML is already sanitized by
	// renderChatMarkdownToSafeHTML.
	answerHTML, mdErr := renderChatMarkdownToSafeHTML(answerWithInlineLinks)
	if mdErr != nil {
		// Fall back to escaped plaintext so the user still sees
		// the assistant's reply if markdown rendering blows up.
		answerHTML = template.HTMLEscapeString(answerWithInlineLinks)
	}

	// answerHTML is trusted — markdown was already rendered
	// through bluemonday's sanitizer above. Wrap as
	// template.HTML so html/template doesn't double-escape the
	// tags when we drop it into the page.
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

	limit := 5
	if raw := r.FormValue("top_k"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}

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
		"Title": "RAGabast - Ingest",
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
	docs, err := s.service.ListDocuments(r.Context())
	if err != nil {
		internalError(w, r, "list documents", err)
		return
	}

	s.renderTemplate(w, "documents.html", map[string]any{
		"Title":     "RAGabast - Documents",
		"Documents": docs,
	})
}

func (s *Server) handleStatic(w http.ResponseWriter, _ *http.Request) {
	// For now, serve a basic response
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("Static assets would be served here"))
}

// handleDebugChatSearch is a temporary diagnostic endpoint
// (gated by ragabast.enable_debug_endpoints: true) that runs
// the EXACT same Search the chat handler uses and returns
// each result's populated fields as JSON. Used to diagnose
// the 2026-09-20 chat-link bug without re-reading rendered
// HTML. Will be removed once the user-visible fix is in
// production and verified.
func (s *Server) handleDebugChatSearch(w http.ResponseWriter, r *http.Request) {
	if !s.config.Ragabast.EnableDebugEndpoints {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		q = "docbuilder"
	}
	results, err := s.service.Search(r.Context(), q, 5, service.SearchFilters{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"config":{"docbuilder_base_url":%q,"templates_dir":%q},"results":[`,
		s.config.Ragabast.DocbuilderBaseURL, s.config.Paths.TemplatesDir)
	for i, res := range results {
		if i > 0 {
			_, _ = fmt.Fprint(w, ",")
		}
		_, _ = fmt.Fprintf(w,
			`{"i":%d,"document_title":%q,"content":%q}`,
			i, res.DocumentTitle, res.Content)
	}
	_, _ = fmt.Fprint(w, "]}")
}
