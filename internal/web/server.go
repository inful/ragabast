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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// serviceAPI is the slice of *service.Service that the web layer
// depends on. Keeping it private to this package lets tests
// substitute a fake without touching the service package.
type serviceAPI interface {
	CheckHealth(ctx context.Context) (bool, error)
	IngestDocument(ctx context.Context, content string) (*models.Document, error)
	Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error)
	ListDocuments(ctx context.Context) ([]models.DocumentInfo, error)
	DeleteDocument(ctx context.Context, documentID string) error
	SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (service.FrontmatterSuggestion, error)
	QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error)
	GetNormalizedTags(ctx context.Context) ([]string, error)
	GetNormalizedCategories(ctx context.Context) ([]string, error)
	GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error)
}

// Server represents the web server. HTTP handlers and template
// rendering live in handlers.go; this file only owns the type,
// the constructor, lifecycle, and the route table.
type Server struct {
	config    *config.Config
	service   serviceAPI
	router    *chi.Mux
	server    *http.Server
	templates *template.Template
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

// NewServer creates a new web server.
func NewServer(cfg *config.Config, svc serviceAPI) *Server {
	router := chi.NewRouter()

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

	s.server = &http.Server{
		Addr:    addr,
		Handler: s.router,
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
