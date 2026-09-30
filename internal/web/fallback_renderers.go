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

// pageHeaderData is the per-page nav-bar data every fallback
// template renders at the top of <body>.
//
// Fields:
//
//   - AuthEnabled is true when at least one OAuth provider is
//     configured. When false, the header renders nothing —
//     the historical single-user install has no login
//     concept.
//   - SignedIn is true when the request carries a valid
//     session cookie. The header shows the user's display
//     name and a sign-out form.
//   - ShowSignIn is true when AuthEnabled is true and the
//     user is not signed in. The header shows a "Sign in"
//     link to /auth/login (with ?next=<current path> so the
//     post-login redirect lands the user back where they
//     came from).
//   - DisplayName is the rendered "alice (github)" label
//     when signed in. Empty when not signed in.
//   - CsrfToken is the per-request CSRF token stamped on the
//     context by the csrf middleware. Embedded in the
//     sign-out form so the browser-echoed cookie matches
//     the form value at submit time.
//   - SignInURL is the href for the "Sign in" link —
//     /auth/login?next=<current path>, percent-encoded.
//
// All three string fields (DisplayName, CsrfToken, SignInURL)
// flow through html/template's {{ }} substitution which
// auto-escapes HTML-significant characters; an attacker who
// controls the URL or username can't break out of the
// attribute context.
type pageHeaderData struct {
	AuthEnabled bool
	SignedIn    bool
	ShowSignIn  bool
	DisplayName string
	CsrfToken   string
	SignInURL   string
}

// chatFallbackData is the data shape for the chat landing page.
type chatFallbackData struct {
	Title     string
	CsrfToken string
	SessionID string // issue #22: chat session id, embedded in the form so reloads preserve context
	Header    pageHeaderData
}

// ingestFallbackData is the data shape for the GET /ingest page.
// Error and Content are populated only when the page is re-rendered
// after a failed POST /ingest submission — Error carries the
// human-readable reason the content was rejected, and Content is
// the user's original submission so they can correct and retry
// without losing what they typed.
type ingestFallbackData struct {
	Title     string
	CsrfToken string
	Error     string
	Content   string
	Header    pageHeaderData
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
	Header     pageHeaderData
}

// documentsFallbackData is the data shape for GET /documents.
// Title, Tags, Category, and ID are all user-controllable (parsed
// from ingested frontmatter / H1 header).
//
// Pagination fields (Total, Limit, Offset, PrevOffset, NextOffset,
// StartShowing, EndShowing) drive the Previous/Next links
// rendered at the bottom of the page when the corpus exceeds
// the page size. -1 sentinel on PrevOffset/NextOffset means
// "no link" — the template suppresses the corresponding
// button.
//
// CsrfToken is embedded in every per-row Delete form so the
// handler can verify the POST against the same double-submit
// cookie the rest of the form-mounted endpoints use.
type documentsFallbackData struct {
	Title        string
	CsrfToken    string
	Documents    []documentsFallbackRow
	Total        int
	Limit        int
	Offset       int
	PrevOffset   int
	NextOffset   int
	StartShowing int
	EndShowing   int
	Header       pageHeaderData
}

// documentsFallbackRow is one row of the documents table.
// DisplayLabel is the resolved title (title → filename → id) so
// the template can show a friendly identifier even when the
// parent document has no H1; computed by the handler that builds
// the row so the template stays presentation-only.
//
// The Delete form is rendered inline with the row — POST to
// /documents/{id}/delete with the csrf token and a required
// `confirm=1` checkbox. The browser-side required attribute is
// the primary guard against accidental deletes; the handler
// also enforces it server-side so a forged POST that omits the
// checkbox gets a 400.
type documentsFallbackRow struct {
	DisplayLabel string
	Title        string
	ID           string
	Tags         []string
	Category     string
	Chunks       int
	CsrfToken    string
}

// newFallbackTemplates parses every fallback template body.
// Parse errors are surfaced at startup so a broken template is
// caught in `serve` rather than at first request.
//
// Bodies are intentionally defined as raw strings so html/template
// can apply context-aware escaping to every {{ }} expression; do
// not refactor to fmt.Fprintf.
//
// The header partial is shared across all four pages so the
// sign-in / sign-out UX stays consistent without duplicating
// the navbar HTML four times. Each per-page template is
// built by composing pageHeaderFallbackBody (the named block
// "header") with the page-specific body string at parse time.
func newFallbackTemplates() *fallbackTemplates {
	return &fallbackTemplates{
		chat:          mustParseWithHeader("chat.html", chatFallbackBody),
		ingest:        mustParseWithHeader("ingest.html", ingestFallbackBody),
		ingestSuccess: mustParseWithHeader("ingest_success.html", ingestSuccessFallbackBody),
		documents:     mustParseWithHeader("documents.html", documentsFallbackBody),
	}
}

// mustParseWithHeader builds one fallback template by combining
// the shared header partial with the page-specific body. The
// result is a single *template.Template that exposes the named
// block "header" so per-page bodies can include it via
// {{ template "header" .Header }}.
func mustParseWithHeader(name, body string) *template.Template {
	t := template.Must(template.New(name).Parse(pageHeaderFallbackBody))
	return template.Must(t.Parse(body))
}

// pageHeaderFallbackBody defines the "header" block every page
// includes at the top of <body>. The block renders nothing
// when AuthEnabled is false (the historical single-user
// install has no login concept); when true, it shows the
// signed-in user + sign-out button, OR the "Sign in" link
// The basic nav (Chat | Search | Documents | Ingest) renders on
// every page regardless of OAuth configuration — operators
// running ragabast locally without configuring auth can still
// navigate between sections without typing URLs or clicking
// per-page Back buttons. When auth is enabled, the user
// display name and Sign in / Sign out bits render on the
// right-hand end of the nav, on top of the basic links.
//
// Bulma navbar markup keeps the visual language consistent
// with the rest of the page chrome (chat-message, search,
// search-results partials all use bulma classes too).
//
// CSRF: the sign-out form carries a csrf_token hidden field.
// The csrf middleware sets the matching cookie on every
// response; on submit, the middleware compares the form
// value to the cookie and rejects mismatches with 403.
// A signed-out browser can still submit the form (the
// cookie is cleared), but the next render of any page
// will be the public path and the cookie value won't
// matter — the csrf middleware is a no-op for the
// single-user open-access install (AuthEnabled=false).
const pageHeaderFallbackBody = `
{{ define "header" -}}
<nav class="navbar is-light" role="navigation" aria-label="main navigation">
  <div class="navbar-brand">
    <a class="navbar-item" href="/">ragabast</a>
  </div>
  <div class="navbar-menu is-active">
    <div class="navbar-start">
      <a class="navbar-item" href="/">Chat</a>
      <a class="navbar-item" href="/search">Search</a>
      <a class="navbar-item" href="/documents">Documents</a>
      <a class="navbar-item" href="/ingest">Ingest</a>
    </div>
    {{- if .AuthEnabled }}
    <div class="navbar-end">
      {{- if .SignedIn }}
      <span class="navbar-item has-text-grey">{{ .DisplayName }}</span>
      <div class="navbar-item">
        <form method="post" action="/auth/logout">
          <input type="hidden" name="csrf_token" value="{{ .CsrfToken }}">
          <button class="button is-small is-light" type="submit">Sign out</button>
        </form>
      </div>
      {{- else if .ShowSignIn }}
      <a class="navbar-item" href="{{ .SignInURL }}">Sign in</a>
      {{- end }}
    </div>
    {{- end }}
  </div>
</nav>
{{- end -}}
`

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
//
// CSP compliance:
//   - Styles live in /static/chat.css (no inline <style>
//     block; the strict CSP forbids 'unsafe-inline').
//   - The htmx config meta tag disables eval()/Function() so
//     htmx never tries to evaluate hx-on::* attributes. The
//     loading-state UX that used to be hx-on::* is wired
//     instead by /static/chat.js via DOM event listeners.
const chatFallbackBody = `<!DOCTYPE html>
<html>
<head>
	<title>{{ .Title }}</title>
	<meta name="htmx-config" content='{"allowEval":false}'>
	<link rel="stylesheet" href="/static/bulma.min.css">
	<link rel="stylesheet" href="/static/chat.css">
	<script src="/static/htmx.min.js" defer></script>
	<script src="/static/chat.js" defer></script>
</head>
<body class="container mt-4">
	{{ template "header" .Header }}
	<h1 class="title">Chat</h1>
	<p class="subtitle">Ask questions against the ingested documents.</p>

	<div id="chat-messages" class="box chat-log">
		<div class="content" id="chat-messages-placeholder">
			<p class="has-text-grey">No messages yet.</p>
		</div>
		<button id="jump-to-latest" class="button is-small jump-to-latest" type="button" aria-label="Jump to latest message">Jump to latest ↓</button>
	</div>

	<div class="box">
		<a id="chat-export" class="button is-small is-light" href="/api/chat/export?session_id={{ .SessionID }}">Export transcript (.md)</a>
	</div>

	<form id="chat-form" class="box" hx-post="/chat/message" hx-target="#chat-messages" hx-swap="beforeend" hx-indicator="#chat-indicator" hx-disabled-elt="#chat-send, #chat-input">
		<input type="hidden" name="csrf_token" value="{{ .CsrfToken }}">
		<input type="hidden" name="session_id" value="{{ .SessionID }}">
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

// ingestFallbackBody renders the GET /ingest page. When Error is
// non-empty (a failed POST re-render), the form shows a Bulma
// `is-danger` notification with the human-readable reason and
// pre-fills the textarea with the user's original submission so
// they can correct and retry without losing what they typed.
const ingestFallbackBody = `<!DOCTYPE html>
<html>
<head>
    <title>{{ .Title }}</title>
    <link rel="stylesheet" href="/static/bulma.min.css">
</head>
<body class="container mt-4">
    {{ template "header" .Header }}
    <h1 class="title">Ingest Document</h1>
    {{ if .Error }}
    <div class="notification is-danger">
        <strong>Ingest failed:</strong> {{ .Error }}
    </div>
    {{ end }}
    <form method="post" action="/ingest">
        <input type="hidden" name="csrf_token" value="{{ .CsrfToken }}">
        <div class="field">
            <label class="label">Docbuilder Content</label>
            <div class="control">
                <textarea class="textarea" name="content" rows="15" placeholder="Paste your docbuilder markdown content here..." required>{{ .Content }}</textarea>
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
    <link rel="stylesheet" href="/static/bulma.min.css">
</head>
<body class="container mt-4">
    {{ template "header" .Header }}
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
//
// Each row carries a Delete form that POSTs to
// /documents/{id}/delete. The form requires the operator to
// tick a `confirm` checkbox before submit (browser-side guard);
// the handler also enforces the checkbox server-side (defense
// in depth against a forged POST). CSRF token is embedded per
// row; the csrf middleware already rejects mismatches.
const documentsFallbackBody = `<!DOCTYPE html>
<html>
<head>
    <title>{{ .Title }}</title>
    <link rel="stylesheet" href="/static/bulma.min.css">
</head>
<body class="container mt-4">
    {{ template "header" .Header }}
    <h1 class="title">Ingested Documents</h1>
    <a href="/" class="button is-light mb-4">Back</a>
    {{ if .Documents }}
    <table class="table is-fullwidth is-striped">
        <thead><tr><th>Title</th><th>ID</th><th>Tags</th><th>Category</th><th>Chunks</th><th></th></tr></thead>
        <tbody>
        {{ range .Documents }}
            <tr>
                <td>{{ .DisplayLabel }}</td>
                <td><code>{{ .ID }}</code></td>
                <td>{{ .Tags }}</td>
                <td>{{ .Category }}</td>
                <td>{{ .Chunks }}</td>
                <td>
                    <form method="post" action="/documents/{{ .ID }}/delete" style="display:inline">
                        <input type="hidden" name="csrf_token" value="{{ .CsrfToken }}">
                        <label class="checkbox is-small">
                            <input type="checkbox" name="confirm" value="1" required> confirm
                        </label>
                        <button class="button is-small is-danger" type="submit">Delete</button>
                    </form>
                </td>
            </tr>
        {{ end }}
        </tbody>
    </table>
    {{ else }}
    <p>No documents ingested yet.</p>
    {{ end }}

    {{ if .Total }}
    <p class="has-text-grey mt-4">
        Showing {{ .StartShowing }}–{{ .EndShowing }} of {{ .Total }}
        (page size {{ .Limit }}).
        {{ if gt .PrevOffset -1 }}
        <a class="button is-small ml-2" href="/documents?limit={{ .Limit }}&offset={{ .PrevOffset }}">Previous</a>
        {{ end }}
        {{ if gt .NextOffset -1 }}
        <a class="button is-small ml-2" href="/documents?limit={{ .Limit }}&offset={{ .NextOffset }}">Next</a>
        {{ end }}
    </p>
    {{ end }}
</body>
</html>`
