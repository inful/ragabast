package web

import (
	"html/template"
	"net/http"
)

// fallbackTemplates holds pre-parsed html/template instances
// for every page the unsafe serveBasicHTML path would otherwise
// hand-roll with fmt.Fprintf. The fallback fires when the
// embedded template set does not include the requested page name
// (the production Docker image today, which only ships
// chat_message.html, search.html, and search_results.html).
//
// Every fallback template is parsed once at server construction
// and executed via html/template, which contextually escapes
// every {{ .Field }} substitution. This is the same defense the
// embedded templates rely on; the fallback path used to bypass
// it with raw fmt.Fprintf %s/%v, which produced stored XSS via
// any user-controllable string (doc.Title from the first H1
// header, doc.Tags from the YAML `tags:` array).
//
// The page bodies are kept close to the previous fmt.Fprintf
// output to minimize visual drift; the security-relevant change
// is the renderer, not the markup.
type fallbackTemplates struct {
	chat          *template.Template
	ingest        *template.Template
	ingestSuccess *template.Template
	documents     *template.Template
}

// chatFallbackData is the data shape for the chat landing page.
type chatFallbackData struct {
	Title string
}

// ingestFallbackData is the data shape for the GET /ingest page.
type ingestFallbackData struct {
	Title string
}

// ingestSuccessFallbackData is the data shape for POST /ingest's
// success page. DocumentID comes from the docbuilder UID
// (user-controllable in YAML frontmatter), so the renderer MUST
// escape it; the same applies to Tags.
type ingestSuccessFallbackData struct {
	Title      string
	DocumentID string
	Chunks     int
	Tags       []string
}

// documentsFallbackData is the data shape for GET /documents.
// Title, Tags, Category, and ID are all user-controllable (parsed
// from ingested frontmatter / H1 header).
type documentsFallbackData struct {
	Title     string
	Documents []documentsFallbackRow
}

// documentsFallbackRow is one row of the documents table.
type documentsFallbackRow struct {
	Title    string
	ID       string
	Tags     []string
	Category string
	Chunks   int
}

// newFallbackTemplates parses every fallback template body.
// Parse errors are surfaced at startup so a broken template is
// caught in `serve` rather than at first request.
//
// Bodies are intentionally defined as raw strings so html/template
// can apply context-aware escaping to every {{ }} expression; do
// not refactor to fmt.Fprintf.
func newFallbackTemplates() *fallbackTemplates {
	return &fallbackTemplates{
		chat:          template.Must(template.New("chat.html").Parse(chatFallbackBody)),
		ingest:        template.Must(template.New("ingest.html").Parse(ingestFallbackBody)),
		ingestSuccess: template.Must(template.New("ingest_success.html").Parse(ingestSuccessFallbackBody)),
		documents:     template.Must(template.New("documents.html").Parse(documentsFallbackBody)),
	}
}

// renderFallback executes the named fallback template with the
// supplied data. Content-Type is set to text/html; charset=utf-8
// before the template writes any bytes so partial failure leaves
// a correct header rather than a default.
func (s *Server) renderFallback(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	var tmpl *template.Template
	switch name {
	case "chat.html":
		tmpl = s.fallback.chat
	case "ingest.html":
		tmpl = s.fallback.ingest
	case "ingest_success.html":
		tmpl = s.fallback.ingestSuccess
	case "documents.html":
		tmpl = s.fallback.documents
	default:
		http.NotFound(w, nil)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		internalError(w, nil, "fallback render", err)
	}
}

// chatFallbackBody renders the chat landing page.
//
// Security: every user-controllable value flows through
// html/template's {{ }} context, which escapes HTML-significant
// characters automatically.
const chatFallbackBody = `<!DOCTYPE html>
<html>
<head>
	<title>{{ .Title }}</title>
	<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
	<script src="https://unpkg.com/htmx.org@1.9.10"></script>
	<style>
		.chat-log { max-height: 60vh; overflow-y: auto; }
		.chat-msg { max-width: 100%; overflow-wrap: anywhere; word-break: break-word; }
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
</body>
</html>`

// ingestFallbackBody renders the GET /ingest page.
const ingestFallbackBody = `<!DOCTYPE html>
<html>
<head>
    <title>{{ .Title }}</title>
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
</html>`

// ingestSuccessFallbackBody renders the POST /ingest confirmation.
//
// Security: DocumentID comes from the docbuilder UID in the
// ingested frontmatter; Tags is parsed from the YAML `tags:`
// array. Both MUST pass through html/template's auto-escaping.
const ingestSuccessFallbackBody = `<!DOCTYPE html>
<html>
<head>
    <title>{{ .Title }}</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
</head>
<body class="container mt-4">
    <div class="notification is-success">
        <h1 class="title">Document Ingested Successfully!</h1>
        <p><strong>Document ID:</strong> {{ .DocumentID }}</p>
        <p><strong>Chunks Created:</strong> {{ .Chunks }}</p>
        <p><strong>Tags:</strong> {{ .Tags }}</p>
    </div>
    <div class="buttons">
        <a href="/ingest" class="button is-primary">Ingest Another</a>
        <a href="/search" class="button is-info">Search</a>
        <a href="/" class="button is-light">Home</a>
    </div>
</body>
</html>`

// documentsFallbackBody renders the GET /documents page.
//
// Security: every row's Title (H1 header), ID (UID), Tags
// (frontmatter tags), and Category (frontmatter categories)
// are attacker-controllable. html/template escapes them all.
const documentsFallbackBody = `<!DOCTYPE html>
<html>
<head>
    <title>{{ .Title }}</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
</head>
<body class="container mt-4">
    <h1 class="title">Ingested Documents</h1>
    <a href="/" class="button is-light mb-4">Back</a>
    {{ if .Documents }}
    <table class="table is-fullwidth is-striped">
        <thead><tr><th>Title</th><th>ID</th><th>Tags</th><th>Category</th><th>Chunks</th></tr></thead>
        <tbody>
        {{ range .Documents }}
            <tr>
                <td>{{ .Title }}</td>
                <td><code>{{ .ID }}</code></td>
                <td>{{ .Tags }}</td>
                <td>{{ .Category }}</td>
                <td>{{ .Chunks }}</td>
            </tr>
        {{ end }}
        </tbody>
    </table>
    {{ else }}
    <p>No documents ingested yet.</p>
    {{ end }}
</body>
</html>`
