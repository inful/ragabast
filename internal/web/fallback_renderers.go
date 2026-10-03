package web

import (
	"html/template"
	"net/http"
	"time"

	"github.com/ragabast/internal/models"
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
// any user-controllable string (doc.Title from the frontmatter
// `title:` field, doc.Tags from the YAML `tags:` array).
//
// The page bodies are kept close to the previous fmt.Fprintf
// output to minimize visual drift; the security-relevant change
// is the renderer, not the markup.
type fallbackTemplates struct {
	chat      *template.Template
	documents *template.Template
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
	Title         string
	CsrfToken     string
	SessionID     string // issue #22: chat session id, embedded in the form so reloads preserve context
	Header        pageHeaderData
	SourceKinds   []models.SourceKind // Stage 2.5: closed set of source kinds the multi-select renders
	SelectedKinds []string            // Stage 2.5: per-session sticky default; pre-fills the checkboxes
}

// documentsFallbackData is the data shape for GET /documents.
// Title, Tags, Category, and ID are all user-controllable (parsed
// from the ingested frontmatter — specifically the `title:` field
// for Title, the `tags:` array for Tags, etc.).
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
	// IngestedAt is the parent document's creation
	// time. The zero value means the upstream path
	// didn't set it (a document ingested before this
	// field was tracked, or a fixture in a test); the
	// template substitutes a placeholder for that case
	// rather than render Go's zero-value literal
	// "0001-01-01".
	IngestedAt time.Time
	CsrfToken  string
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
// sign-in / sign-out UX stays consistent without duplicating the
// navbar HTML four times. Each per-page template is
// built by composing pageHeaderFallbackBody (the named block
// "header") with the page-specific body string at parse time.
//
// parseFallback is the FuncMap-bearing parser that
// NewServer uses for every fallback template. The asset
// FuncMap closure captures s.assetURL from the surrounding
// Server, so this helper lives in fallback_renderers.go
// (where the per-page body strings live) and is called from
// server.go after s.assetVersions is populated.
//
// Why the FuncMap is a parameter rather than a closure:
//
//	The asset-busting query string (`{{ asset "..." }}`) is
//	embedded in the rendered templates, so the `asset`
//	function must be in scope at Parse time — not after, as
//	Funcs-after-Parse does for a pre-parsed template. The
//	FuncMap is a parameter so the helper stays pure and the
//	Server passes its assetURL closure once at construction.
func parseFallback(name, body string, funcs template.FuncMap) *template.Template {
	t := template.Must(template.New(name).Funcs(funcs).Parse(pageHeaderFallbackBody))
	return template.Must(t.Parse(body))
}

// pageHeaderFallbackBody is the navbar HTML wrapped in a
// `header` template block. NewServer parses this into the
// embedded template set so per-page embedded templates can
// invoke it via {{ template "header" .Header }}; the fallback
// renderer parses this with the page body string in
// mustParseWithHeader.
//
// The basic links (Chat | Search | Documents) render on
// every page regardless of OAuth configuration; the user
// display name and Sign in / Sign out bits render on top
// when auth is also enabled. Issue #85. The HTML /ingest
// form was removed in PR 2 (see plans/remove-ingest-form.md);
// ingest is reachable via the Huma HTTP API
// (/api/ingest, /api/ingest/raw, /api/ingest/file) so the
// link was no longer pointing at a resource.
//
// daisyUI navbar markup keeps the visual language consistent
// with the rest of the page chrome (chat-message, search,
// search-results partials all use daisyUI classes too).
//
// Phase 2f of the Bulma -> DaisyUI migration: the navbar
// pattern moves from Bulma's navbar-brand / navbar-menu /
// navbar-item to daisyUI's navbar / navbar-start /
// navbar-end. daisyUI doesn't have a "menu" concept —
// the nav items just sit as flex children of
// navbar-start / navbar-end. The nav-item buttons use
// daisyUI's `btn btn-ghost` so they pick up the same
// hover/active treatment as the rest of the app's
// buttons. The brand text uses `text-xl` for the
// pre-migration `is-size-4` size.
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
<!--
  Phase 5.4 of plans/ux-overhaul.md: the brand bar
  pairs a small inline SVG mark with the "ragabast"
  text. The daisyUI brand-bar pattern uses a logo as
  the visual anchor and the text as the label; the
  mark follows the link's text color in both themes
  via fill="currentColor". The path is a simple
  speech-bubble shape (ragabast is a chat product).
  A future redesign of the mark is fine; the
  contract is "the brand bar has an SVG mark next to
  the text".
-->
<nav class="navbar bg-base-100 shadow-sm sticky top-0 z-10" role="navigation" aria-label="main navigation">
  <div class="navbar-start">
    <a class="btn btn-ghost text-xl" href="/">
      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="size-5 shrink-0" aria-hidden="true">
        <path d="M4 4h16a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H8l-4 4V6a2 2 0 0 1 2-2z"/>
      </svg>
      ragabast
    </a>
  </div>
  <div class="navbar-end gap-1">
    <a class="btn btn-ghost btn-sm" href="/">Chat</a>
    <a class="btn btn-ghost btn-sm" href="/search">Search</a>
    <a class="btn btn-ghost btn-sm" href="/documents">Documents</a>
    {{- if .AuthEnabled }}
    {{- if .SignedIn }}
    <span class="text-base-content/60 px-2">{{ .DisplayName }}</span>
    <form method="post" action="/auth/logout">
      <input type="hidden" name="csrf_token" value="{{ .CsrfToken }}">
      <button class="btn btn-sm" type="submit">Sign out</button>
    </form>
    {{- else if .ShowSignIn }}
    <a class="btn btn-ghost btn-sm" href="{{ .SignInURL }}">Sign in</a>
    {{- end }}
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
	<link rel="stylesheet" href="{{ asset "daisyui.min.css" }}">
	<link rel="stylesheet" href="{{ asset "chat.css" }}">
	<script src="{{ asset "htmx.min.js" }}" defer></script>
	<script src="{{ asset "chat.js" }}" defer></script>
</head>
<body class="container mx-auto mt-4">
	{{ template "header" .Header }}
	<h1 class="text-2xl font-semibold">Chat</h1>
	<p class="text-base text-base-content/70 mb-4">Retrieval-augmented chat: each reply cites the chunks it was grounded on.</p>

	<div id="chat-messages" class="card chat-log" role="log" aria-live="polite" aria-label="Chat transcript">
		<div class="card-body">
			<div class="prose" id="chat-messages-placeholder">
				<p class="text-base-content/60">Chat with your ingested documents. Type a question below; each reply cites the chunks it was grounded on.</p>
			</div>
		</div>
		<button id="jump-to-latest" class="btn btn-sm jump-to-latest" type="button" aria-label="Jump to latest message">Jump to latest ↓</button>
	</div>

	<form id="chat-form" class="card mt-4" hx-post="/chat/message" hx-target="#chat-messages" hx-swap="beforeend" hx-indicator="#chat-indicator" hx-disabled-elt="#chat-send, #chat-input">
		<input type="hidden" name="csrf_token" value="{{ .CsrfToken }}">
		<input type="hidden" name="session_id" value="{{ .SessionID }}">
		<div class="card-body space-y-4">
			<fieldset class="fieldset">
				<legend class="fieldset-legend">Message</legend>
				<textarea id="chat-input" class="textarea w-full" name="message" rows="2" placeholder="Ask a question..." required></textarea>
				<p class="label">Press Ctrl+Enter (Cmd+Enter on macOS) to send. Enter inserts a newline.</p>
			</fieldset>
			<fieldset class="fieldset">
				<legend class="fieldset-legend">Sources</legend>
				<div class="flex flex-col gap-2">
					{{ $selected := .SelectedKinds }}
					{{ range .SourceKinds }}
					{{ $kind := . }}
					<label class="flex items-center gap-2 cursor-pointer">
						<input type="checkbox" name="source_kinds" value="{{ $kind }}" class="checkbox checkbox-sm"
							{{ if eq (len $selected) 0 }}checked{{ end }}
							{{ range $selected }}{{ if eq . $kind }}checked{{ end }}{{ end }}>
						<span class="source-kind-emoji">{{ $kind.InlineSourceIcon }}</span>
						<span class="source-kind-name">{{ $kind.DisplayName }}</span>
					</label>
					{{ end }}
				</div>
				<p class="label">Empty selection = all sources. Your choice persists across messages in this session; unchecking all boxes does NOT clear the stored default.</p>
			</fieldset>
			<div class="flex items-center gap-2">
				<button id="chat-send" class="btn btn-primary" type="submit">Send</button>
				<div class="htmx-indicator" id="chat-indicator">
					<span class="badge">Thinking…</span>
				</div>
			</div>
		</div>
	</form>
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
    <link rel="stylesheet" href="{{ asset "daisyui.min.css" }}">
    <link rel="stylesheet" href="{{ asset "chat.css" }}">
    <script src="{{ asset "htmx.min.js" }}" defer></script>
    <script src="{{ asset "chat.js" }}" defer></script>
</head>
<body class="container mx-auto mt-4">
    {{ template "header" .Header }}
    <h1 class="text-2xl font-semibold">Ingested Documents</h1>
    {{ if .Documents }}
    <table class="table table-zebra w-full">
        <thead><tr><th scope="col">Title</th><th scope="col">ID</th><th scope="col">Tags</th><th scope="col">Category</th><th scope="col">Chunks</th><th scope="col">Ingested</th><th scope="col"></th></tr></thead>
        <tbody>
        {{ range .Documents }}
            <tr>
                <td>{{ .DisplayLabel }}</td>
                <td><code>{{ .ID }}</code></td>
                <td>
                    {{- range .Tags }}
                    <span class="badge badge-info">{{ . }}</span>
                    {{- end }}
                </td>
                <td>{{ .Category }}</td>
                <td>{{ .Chunks }}</td>
                <td>
                    {{- if .IngestedAt.IsZero }}
                    <span class="text-base-content/60">unknown</span>
                    {{- else }}
                    <time datetime="{{ .IngestedAt.Format "2006-01-02T15:04:05Z07:00" }}">{{ .IngestedAt.Format "2006-01-02 15:04 UTC" }}</time>
                    {{- end }}
                </td>
                <td>
                    <form method="post" action="/documents/{{ .ID }}/delete" class="inline-flex items-center gap-2">
                        <input type="hidden" name="csrf_token" value="{{ .CsrfToken }}">
                        <label class="flex items-center gap-1 text-sm">
                            <input type="checkbox" name="confirm" value="1" required class="checkbox checkbox-sm"> confirm
                        </label>
                        <button class="btn btn-sm btn-error" type="submit">Delete</button>
                    </form>
                </td>
            </tr>
        {{ end }}
        </tbody>
    </table>
    {{ else }}
    <div class="alert">
        No documents ingested yet. Submit one through <code>POST /api/ingest</code> to add some.
    </div>
    {{ end }}

    {{ if .Total }}
    <p class="text-base-content/60 mt-4">
        Showing {{ .StartShowing }}–{{ .EndShowing }} of {{ .Total }}
        (page size {{ .Limit }}).
        {{ if gt .PrevOffset -1 }}
        <a class="btn btn-sm ml-2" href="/documents?limit={{ .Limit }}&offset={{ .PrevOffset }}">Previous</a>
        {{ end }}
        {{ if gt .NextOffset -1 }}
        <a class="btn btn-sm ml-2" href="/documents?limit={{ .Limit }}&offset={{ .NextOffset }}">Next</a>
        {{ end }}
    </p>
    {{ end }}
</body>
</html>`

// headerFromMap extracts the optional pageHeaderData the
// handler stashed under the "Header" key. Missing or
// wrong-typed → empty struct (the template renders nothing
// when AuthEnabled is false, which is the historical
// single-user open-access behavior).
func headerFromMap(data any) pageHeaderData {
	m, ok := data.(map[string]any)
	if !ok {
		return pageHeaderData{}
	}
	h, _ := m["Header"].(pageHeaderData)

	return h
}

// titleFromMap returns the Title field with a fallback
// when missing, empty, or wrong-typed. The fallback is
// what serveBasicHTML would have hardcoded into the
// fmt.Fprintf output before the html/template refactor.
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

// docsRowsFromMap adapts the []models.DocumentInfo slice that
// handleDocumentsPage passes through into the strongly-typed
// documentsFallbackRow slice that the fallback template expects.
// DisplayLabel is computed here (via models.DocumentInfo.DisplayLabel)
// so the template stays presentation-only; the same fallback chain
// powers chat sources and search results. The CSRF token is read
// once and threaded into every row so each delete form embeds it
// without the template doing a context lookup per row.
//
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
	csrf := stringFromMap(data, "CsrfToken")
	out := make([]documentsFallbackRow, 0, len(raw))
	for _, item := range raw {
		out = append(out, documentsFallbackRow{
			DisplayLabel: item.DisplayLabel(),
			Title:        item.Title,
			ID:           item.ID,
			Tags:         item.Tags,
			Category:     firstOrEmpty(item.Categories),
			Chunks:       item.ChunkCount,
			IngestedAt:   item.CreatedAt,
			CsrfToken:    csrf,
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
