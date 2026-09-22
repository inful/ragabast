package web

import (
	"context"
	"fmt"
	"html/template"
	"log"
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
	"github.com/ragabast/internal/web/jobs"
)

// Server represents the web server. HTTP handlers and template
// rendering live in handlers.go; this file only owns the type,
// the constructor, lifecycle, and the route table.
type Server struct {
	config    *config.Config
	service   serviceAPI
	router    *chi.Mux
	server    *http.Server
	templates *template.Template
	// fallback holds pre-parsed html/template instances for
	// every page the embedded template set does not cover.
	// serveBasicHTML uses them so the fallback path stays
	// safe-by-default (every {{ }} substitution is escaped).
	fallback *fallbackTemplates
	// ingestQueue is the async ingest job queue. nil when
	// server.async_ingest_queue_dir is empty (operator opted
	// out of async ingest). The HTTP layer returns 503 from
	// /api/ingest/async when this is nil.
	ingestQueue *jobs.Queue
}

// internalError logs the underlying error and returns a generic 500 to the
// client. The full error chain is preserved in the server log so operators
// can diagnose without exposing internal details (model names, server URLs,
// stack traces) to the user.
func internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	if err != nil {
		log.Printf("server: %s %s: %v", op, r.URL.Path, err)
	}
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

// NewServer creates a new web server.
func NewServer(cfg *config.Config, svc serviceAPI) *Server {
	router := chi.NewRouter()

	// securityHeadersMiddleware runs first so the defense
	// headers land on every response — including error
	// responses from middleware deeper in the chain.
	router.Use(securityHeadersMiddleware)
	// requestIDMiddleware runs second so every downstream
	// middleware, log line, and handler can read the ID via
	// RequestIDFromContext. Placing it before the access
	// logger and recoverer means panic logs and access lines
	// are correlated; placing it after securityHeaders
	// keeps the defense-header layer dependency-free.
	router.Use(requestIDMiddleware())
	// redactAccessLogMiddleware MUST run before
	// middleware.Logger so chi sees the rewritten URL when
	// it formats the access line. The middleware mutates
	// r.URL.RawQuery in place (replacing values for known
	// sensitive keys with "[REDACTED]") so the operator
	// query/messages/text fields never land in the log.
	router.Use(redactAccessLogMiddleware)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RealIP)
	// clientIPMiddleware runs immediately after chi's RealIP
	// so r.RemoteAddr has already been rewritten from
	// X-Forwarded-For / X-Real-IP headers. The middleware
	// extracts the host portion and stores it on the
	// request context; downstream callers (audit log,
	// rate-limit) read it via ClientIPFromContext.
	router.Use(clientIPMiddleware())
	router.Use(maxBytesReaderMiddleware)
	router.Use(middleware.Timeout(60 * time.Second))

	// CORS runs before auth so OPTIONS preflight requests do
	// not require a bearer token (browsers do not send
	// credentials on preflight). The new allow-list middleware
	// replaces the previous wildcard-or-nothing behavior.
	if cfg.Server.EnableCORS {
		router.Use(corsMiddleware(cfg.Server.CORSOrigins))
	}

	// Auth runs last so the body-size limit, security
	// headers, CORS preflight, and request logging all apply
	// to auth-failed requests too. With auth_token empty
	// (the default) the middleware is a no-op so the
	// single-user local install keeps working.
	// EffectiveAuthTokens merges the singular AuthToken
	// (backward-compatible shortcut) with the modern
	// AuthTokens list, deduping by Value. NewServer is the
	// only call site for authMiddleware — keep it that way
	// so the security boundary is easy to audit.
	router.Use(authMiddleware(cfg.Server.EffectiveAuthTokens()))

	// csrfMiddleware runs after auth so a Bearer-auth POST
	// (which cannot be made cross-origin by a browser) skips
	// the CSRF check entirely. It runs before the rate
	// limiter so a CSRF-failed flood still consumes bucket
	// tokens — a defense-in-depth choice that keeps a
	// malicious page from probing token guesses with no
	// rate-limit cost.
	// csrfMiddleware is auth-aware: when EffectiveAuthTokens
	// is empty (the default local-dev install), CSRF is
	// also bypassed because there is nothing to CSRF.
	// When auth is configured, CSRF runs after auth so a
	// Bearer-auth POST (which cannot be made cross-origin
	// by a browser) skips the CSRF check entirely.
	router.Use(csrfMiddleware(cfg.Server.EffectiveAuthTokens()))

	// Rate-limit middleware for the LLM-backed endpoints. The
	// middleware is path-aware (see rate_limit.go) and a no-op
	// for any non-LLM URL prefix; it is installed at the
	// chain level so it covers both the Huma-mounted
	// endpoints and the form-mounted endpoints without
	// wrapping each route individually. The limiter is
	// constructed below in the Server literal so it can be
	// referenced from the route handlers too.
	rateLimiter := newLLMRateLimiter(cfg.Server.RateLimitPerMinute, cfg.Server.RateLimitBurst)
	router.Use(rateLimiter.llmPathMiddleware)

	// Load templates in this order of precedence:
	//   1. cfg.Paths.TemplatesDir, if set (operator override via
	//      yaml / env TEMPLATES_DIR; intended for shipping a custom
	//      template set without rebuilding the binary)
	//   2. The embedded templatesFS (compile-time embed; default
	//      for the published Docker image, which has no templates/
	//      on disk)
	//
	// Both branches parse into the same *template.Template, so
	// the rest of the package doesn't care which path served the
	// templates.
	var templates *template.Template
	var err error
	switch {
	case cfg.Paths.TemplatesDir != "":
		templates, err = template.ParseGlob(filepath.Join(cfg.Paths.TemplatesDir, "*.html"))
		if err != nil {
			log.Printf("web: failed to load templates from %s: %v; falling back to embedded templates", cfg.Paths.TemplatesDir, err)
			templates, err = template.ParseFS(templatesFS, "templates/*.html")
		}
	default:
		templates, err = template.ParseFS(templatesFS, "templates/*.html")
	}
	if err != nil {
		log.Printf("web: failed to load templates: %v", err)
		templates = template.New("base")
	}

	// Async ingest queue. The queue is the source of truth
	// for /api/ingest/async: submissions land here, a
	// bounded worker pool drains it, every state transition
	// is persisted to <cfg.Server.AsyncIngestQueueDir>. A
	// restart re-enqueues any pending/processing jobs.
	//
	// Empty AsyncIngestQueueDir disables async ingest — the
	// /api/ingest/async endpoint returns 503 in that case.
	// The synchronous POST /api/ingest path is unaffected.
	//
	// The queue's IngestDocument adapter wraps the
	// serviceAPI; we hand it svc so workers can call
	// IngestDocument without reaching back into the
	// web-package's internals.
	var ingestQueue *jobs.Queue
	// Reset the package-scope queue reference so a server
	// constructed without async ingest sees ingest_queue.enabled
	// = false in /api/health/full. Without this reset, a
	// previous test that enabled the queue would leak its
	// state into the next test's /api/health/full response.
	globalIngestQueue = nil
	if cfg.Server.AsyncIngestQueueDir != "" {
		q, qerr := jobs.New(
			cfg.Server.AsyncIngestQueueDir,
			cfg.Server.MaxIngestDocumentBytes,
			cfg.Server.AsyncIngestWorkers,
		)
		if qerr != nil {
			log.Printf("web: failed to create ingest queue at %s: %v; async ingest disabled", cfg.Server.AsyncIngestQueueDir, qerr)
		} else {
			ingestQueue = q
			// Stash the queue handle at package scope so the
			// /api/health/full handler can read counters
			// without crossing the serviceAPI boundary twice.
			// Set before Start so the health handler sees the
			// post-Start state once the cleanup loop is up.
			globalIngestQueue = q
			ingestQueue.Start(jobsServiceAdapter{svc: svc}, auditFunc(log.Printf))
			// Background cleanup sweep. Operates on the same
			// audit hook as the worker pool so the operator's
			// log stream is unified. TTL=0 on both knobs
			// disables cleanup entirely; StartCleanup is a
			// no-op in that case.
			ingestQueue.StartCleanup(
				cfg.Server.AsyncIngestCleanupInterval,
				cfg.Server.AsyncIngestCompletedJobTTL,
				cfg.Server.AsyncIngestFailedJobTTL,
				auditFunc(log.Printf),
			)
			log.Printf("web: async ingest queue started at %s (workers=%d, max_document_bytes=%d, cleanup_interval=%s, completed_ttl=%s, failed_ttl=%s)",
				q.Dir(), q.Workers(), q.MaxBytes(),
				cfg.Server.AsyncIngestCleanupInterval,
				cfg.Server.AsyncIngestCompletedJobTTL,
				cfg.Server.AsyncIngestFailedJobTTL)
		}
	}

	s := &Server{
		config:      cfg,
		service:     svc,
		router:      router,
		templates:   templates,
		fallback:    newFallbackTemplates(),
		ingestQueue: ingestQueue,
	}

	s.registerRoutes()

	return s
}

// registerRoutes registers all API routes and web handlers.
//
// Rate limiting: a single per-IP token-bucket middleware is
// installed at the chi level BEFORE routes are registered.
// The middleware is path-aware — it only throttles requests
// matching llmPathPrefixes (see rate_limit.go) and passes
// every other request through. This means one middleware
// covers the Huma-mounted LLM routes (/api/query, /api/search,
// /api/link-suggestions, /api/frontmatter/suggest) AND the
// form-mounted LLM routes (/chat/message, /search), without
// needing to wrap each route individually.
func (s *Server) registerRoutes() {
	registerHumaAPI(s.router, s.config, s.service, s.ingestQueue)

	s.router.Get("/", s.handleChatPage)
	s.router.Get("/chat", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	s.router.Post("/chat/message", s.handleChatMessage)
	s.router.Post("/chat/clear", s.handleChatClear)
	s.router.Get("/api/chat/export", s.handleChatExport)
	s.router.Get("/search", s.handleSearchPage)
	s.router.Post("/search", s.handleSearchSubmit)
	s.router.Get("/ingest", s.handleIngestPage)
	s.router.Post("/ingest", s.handleIngestSubmit)
	s.router.Get("/documents", s.handleDocumentsPage)

	s.router.Get("/static/*", s.handleStatic)
}

// Start starts the web server.
func (s *Server) Start() error {
	addr := s.config.Server.ListenAddr()

	// ReadTimeout/WriteTimeout/IdleTimeout are read from the
	// config but were previously never applied to the
	// http.Server literal — slowloris and slow-body attacks
	// could pin connections open indefinitely. The chi
	// middleware.Timeout cancels the handler context but NOT
	// the underlying connection; only the http.Server timeouts
	// do that.
	//
	// Defaults: ReadTimeout=15s, WriteTimeout=15s,
	// IdleTimeout=120s (the last is fixed, not from config,
	// because no historical config knob exists for it).
	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  s.config.Server.ReadTimeout,
		WriteTimeout: s.config.Server.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		_, _ = fmt.Printf("🚀 Web server starting on http://%s\n", addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			_, _ = fmt.Printf("❌ Server error: %v\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("🛑 Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	return nil
}

// Stop gracefully stops the server.
func (s *Server) Stop(ctx context.Context) error {
	// Drain the async ingest queue FIRST so workers finish
	// whatever jobs are in flight before we close the HTTP
	// listener. (Stop on the queue signals workers to exit
	// after the current job; we don't wait for the queue to
	// empty — that's the operator's responsibility to
	// monitor via the job status endpoint.)
	if s.ingestQueue != nil {
		s.ingestQueue.Stop()
	}

	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// jobsServiceAdapter adapts the web-layer serviceAPI to the
// jobs.Service interface. IngestDocument returns the values
// the job tracker needs (document ID, chunk count) plus any
// error; the queue handles persistence and status transitions.
type jobsServiceAdapter struct {
	svc serviceAPI
}

func (a jobsServiceAdapter) IngestDocument(ctx context.Context, content string) (string, int, error) {
	doc, err := a.svc.IngestDocument(ctx, content)
	if err != nil {
		return "", 0, err
	}
	return doc.ID, len(doc.Chunks), nil
}

// auditFunc adapts a log.Printf-style function to the
// jobs.AuditFunc shape. State transitions are written to the
// standard log so operators tailing the process see them
// without needing a separate audit pipeline.
//
// Format: "ingest job event=<name> key=value ...".
// Keys are job_id, document_id, error, etc. — chosen for
// grep-ability rather than human-friendliness; an operator
// alerting on these should match on `event=ingest.job.failed`
// and surface the job_id.
func auditFunc(logf func(format string, args ...any)) jobs.AuditFunc {
	return func(event string, fields ...any) {
		logf("ingest job event=%s %s", "ingest.job."+event, formatAuditFields(fields))
	}
}

// formatAuditFields renders the key/value pairs AuditFunc
// received as a flat list (k, v, k, v, ...) into a single
// space-separated string. Odd-length input is treated as if
// the last key had an empty value.
func formatAuditFields(fields []any) string {
	out := ""
	var outSb303 strings.Builder
	for i := 0; i < len(fields); i += 2 {
		if i > 0 {
			outSb303.WriteString(" ")
		}
		if i+1 < len(fields) {
			outSb303.WriteString(fmt.Sprintf("%v=%v", fields[i], fields[i+1]))
		} else {
			outSb303.WriteString(fmt.Sprintf("%v=", fields[i]))
		}
	}
	out += outSb303.String()
	return out
}
