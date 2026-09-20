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
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ragabast/internal/config"
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
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RealIP)
	router.Use(maxBytesReaderMiddleware)
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

	s := &Server{
		config:    cfg,
		service:   svc,
		router:    router,
		templates: templates,
		fallback:  newFallbackTemplates(),
	}

	s.registerRoutes()

	return s
}

// registerRoutes registers all API routes and web handlers.
func (s *Server) registerRoutes() {
	registerHumaAPI(s.router, s.config, s.service)

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
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
