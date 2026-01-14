package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

type serviceAPI interface {
	CheckHealth(ctx context.Context) (bool, error)
	IngestDocument(ctx context.Context, content string) (*models.Document, error)
	Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error)
	ListDocuments(ctx context.Context) ([]models.DocumentInfo, error)
	DeleteDocument(ctx context.Context, documentID string) error
	SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (service.FrontmatterSuggestion, error)
	QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error)
	QueryWithLLM(ctx context.Context, query string, model string, history []struct {
		Role    string
		Content string
	}) (string, []struct {
		ID      string
		Content string
		Score   float64
	}, error)
}

// Server represents the web server.
type Server struct {
	config    *config.Config
	service   serviceAPI
	router    *chi.Mux
	server    *http.Server
	templates *template.Template
}

// NewServer creates a new web server.
func NewServer(cfg *config.Config, svc serviceAPI) *Server {
	// Create chi router
	router := chi.NewRouter()

	// Add middleware
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RealIP)
	router.Use(middleware.Timeout(60 * time.Second))

	if cfg.Server.EnableCORS {
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
			})
		})
	}

	// Load templates
	pattern := "templates/*.html"
	if cfg.Paths.TemplatesDir != "" {
		pattern = filepath.Join(cfg.Paths.TemplatesDir, "*.html")
	}
	templates, err := template.ParseGlob(pattern)
	if err != nil {
		// If templates don't exist, create a basic set
		templates = template.New("base")
	}

	s := &Server{
		config:    cfg,
		service:   svc,
		router:    router,
		templates: templates,
	}

	// Register routes
	s.registerRoutes()

	return s
}

// registerRoutes registers all API routes and web handlers.
func (s *Server) registerRoutes() {
	// REST API (Huma).
	registerHumaAPI(s.router, s.config, s.service)

	// Web UI routes
	s.router.Get("/", s.handleChatPage)
	s.router.Get("/chat", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	s.router.Post("/chat/message", s.handleChatMessage)
	s.router.Get("/search", s.handleSearchPage)
	s.router.Post("/search", s.handleSearchSubmit)
	s.router.Get("/ingest", s.handleIngestPage)
	s.router.Post("/ingest", s.handleIngestSubmit)
	s.router.Get("/documents", s.handleDocumentsPage)

	// Static assets
	s.router.Get("/static/*", s.handleStatic)
}

// Start starts the web server.
func (s *Server) Start() error {
	addr := s.config.Server.ListenAddr()

	s.server = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}

	// Start server in goroutine
	go func() {
		_, _ = fmt.Printf("🚀 Web server starting on http://%s\n", addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			_, _ = fmt.Printf("❌ Server error: %v\n", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("🛑 Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	return nil
}

// Stop gracefully stops the server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleSearchPage(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, "search.html", map[string]any{
		"Title": "RAGabast - Search",
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

	answer, _, err := s.service.QueryDebugWithOptions(r.Context(), msg, 5, service.LLMOptions{})
	if err != nil {
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, "chat_message.html", map[string]any{
		"User":   msg,
		"Answer": answer,
	})
}

func (s *Server) handleSearchSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	query := r.FormValue("query")
	if query == "" {
		http.Error(w, "Query is required", http.StatusBadRequest)
		return
	}

	// Perform search
	results, err := s.service.Search(r.Context(), query, 5, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Search failed: %v", err), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, "search_results.html", map[string]any{
		"Title":   "Search Results",
		"Query":   query,
		"Results": results,
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
		http.Error(w, fmt.Sprintf("Ingest failed: %v", err), http.StatusInternalServerError)
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
		http.Error(w, fmt.Sprintf("Failed to list documents: %v", err), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, "documents.html", map[string]any{
		"Title":     "RAGabast - Documents",
		"Documents": docs,
	})
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// For now, serve a basic response
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("Static assets would be served here"))
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
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}

// serveBasicHTML serves basic HTML when templates are not available.
func (s *Server) serveBasicHTML(w http.ResponseWriter, templateName string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Simple HTML templates
	switch templateName {
	case "chat.html":
		s.serveChatHTML(w, data)
	case "chat_message.html":
		s.serveChatMessageHTML(w, data)
	case "search.html":
		s.serveSearchHTML(w, data)
	case "search_results.html":
		s.serveSearchResultsHTML(w, data)
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

func (s *Server) serveChatMessageHTML(w http.ResponseWriter, data any) {
	user := template.HTMLEscapeString(data.(map[string]any)["User"].(string))
	answer, err := renderChatMarkdownToSafeHTML(data.(map[string]any)["Answer"].(string))
	if err != nil {
		answer = template.HTMLEscapeString(data.(map[string]any)["Answer"].(string))
	}

	// Remove placeholder if this is the first message.
	_, _ = fmt.Fprint(w, `<div hx-swap-oob="delete" id="chat-messages-placeholder"></div>`)

	_, _ = fmt.Fprintf(w, `<div class="content">
	<div class="box">
		<p class="has-text-weight-semibold">You</p>
		<pre class="chat-msg">%s</pre>
	</div>
	<div class="box has-background-light">
		<p class="has-text-weight-semibold">Assistant</p>
		<div class="chat-msg content">%s</div>
	</div>
</div>`, user, answer)
}

func (s *Server) serveSearchHTML(w http.ResponseWriter, data any) {
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>%s</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
    <script src="https://unpkg.com/htmx.org@1.9.10"></script>
</head>
<body class="container mt-4">
    <h1 class="title">Search Documents</h1>
    <form method="post" action="/search">
        <div class="field">
            <label class="label">Query</label>
            <div class="control">
                <input class="input" type="text" name="query" placeholder="Enter your search query..." required>
            </div>
        </div>
        <div class="field">
            <div class="control">
                <button class="button is-info" type="submit">Search</button>
                <a href="/" class="button is-light">Back</a>
            </div>
        </div>
    </form>
</body>
</html>`, data.(map[string]any)["Title"])
}

func (s *Server) serveSearchResultsHTML(w http.ResponseWriter, data any) {
	results := data.(map[string]any)["Results"]
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>%s</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
</head>
<body class="container mt-4">
    <h1 class="title">Search Results</h1>
    <p class="subtitle">Query: %s</p>
    <a href="/search" class="button is-light mb-4">New Search</a>
`, data.(map[string]any)["Title"], data.(map[string]any)["Query"])

	if results != nil {
		_, _ = fmt.Fprintf(w, `<div class="columns is-multiline">`)
		for _, r := range results.([]any) {
			result := r.(map[string]any)
			_, _ = fmt.Fprintf(w, `<div class="column is-full">
                <div class="box">
                    <h4 class="title is-4">%s</h4>
                    <p class="subtitle is-6">Document: %s | Similarity: %.3f</p>
                    <div class="content"><p>%s</p></div>
                    <p class="is-size-7">Path: %s (Level %d)</p>
                </div>
            </div>`,
				result["DocumentTitle"],
				result["DocumentID"],
				result["Similarity"],
				result["Content"],
				result["HeaderPath"],
				result["Level"])
		}
		_, _ = fmt.Fprintf(w, `</div>`)
	} else {
		_, _ = fmt.Fprintf(w, `<p>No results found.</p>`)
	}

	_, _ = fmt.Fprintf(w, `</body></html>`)
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
            <label class="label">Docubilder Content</label>
            <div class="control">
                <textarea class="textarea" name="content" rows="15" placeholder="Paste your docubilder markdown content here..." required></textarea>
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
