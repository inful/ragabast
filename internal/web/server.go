package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
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
	"github.com/ragabast/internal/web/jobs"
)

// Server represents the web server. HTTP handlers live in
// feature-specific files (chat_handlers.go, documents_handlers.go,
// search_handlers.go, static.go); template rendering and shared
// page helpers live in handlers.go. This file only owns the type,
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
	// oauthHandlers is the OAuth / OIDC login machinery.
	// nil when server.auth.providers is empty (the
	// historical single-user / bearer-token install). The
	// session store, when oauthHandlers is non-nil, holds
	// authenticated browser sessions keyed by the
	// session-cookie value.
	oauthHandlers *oauthHandlers
	sessions      *sessionStore
	// assetVersions holds filename → hex-sha256 for every
	// static asset the server serves. Populated once at
	// construction by buildAssetVersions and exposed to
	// templates via the `asset` func (see asset_version.go)
	// so every <link href> / <script src> in the rendered
	// HTML carries a "?v=<sha>" query string. That query
	// string is the cache-busting key: a binary upgrade
	// produces a different SHA for any changed file, the
	// URL changes, and the browser refetches despite
	// Cache-Control: immutable + max-age=1y on the static
	// handler. The handler ignores query strings, so the
	// served bytes are unchanged.
	assetVersions map[string]string
}

// internalError logs the underlying error and returns a generic 500 to the
// client. The full error chain is preserved in the server log so operators
// can diagnose without exposing internal details (model names, server URLs,
// stack traces) to the user.
//
// r may be nil: a few render paths (notably the post-template-execution
// error fallback in renderTemplate) don't have a request in scope. Treat
// the request URL as best-effort — the operation label and the underlying
// error are what operators actually need to diagnose.
//
// err may be nil for callers that just want to write a 500 without a
// reason. The function is a no-op in that case — without an underlying
// error there is nothing to log, and writing an unexplained 500 would
// confuse more than it helps. All callers currently pass a non-nil err;
// the nil branch is defense in depth.
func internalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	if err == nil {
		return
	}
	path := "<no-request>"
	if r != nil && r.URL != nil {
		path = r.URL.Path
	}
	log.Printf("server: %s %s: %v", op, path, err)
	http.Error(w, "Internal server error", http.StatusInternalServerError)
}

// NewServer creates a new web server.
//
// The constructor is intentionally a thin orchestrator: each
// step that used to live inline (middleware chain, OAuth
// init, template loading, async ingest queue, asset FuncMap)
// is its own method on *Server. The order matters:
//
//  1. initOAuth — populates s.sessions and s.oauthHandlers.
//     The auth middleware (next step) needs s.sessions and
//     s.oauthHandlers.cookieName, so OAuth init must come
//     first.
//  2. installMiddleware — assembles the chi router's
//     middleware chain. Reads s.sessions and
//     s.oauthHandlers.cookieName.
//  3. loadTemplates — populates s.templates from the
//     operator-supplied dir or the embedded FS.
//  4. initIngestQueue — populates s.ingestQueue (or leaves
//     it nil when async ingest is disabled).
//  5. installAssetFuncs — wires the `asset` template
//     function onto s.templates and parses the fallback
//     Go-string pages into s.fallback.
//  6. registerRoutes — mounts every chi and Huma route.
//
// Each step has a focused test in server_*_test.go; the
// existing integration tests (auth_routes_test.go,
// health_full_test.go, etc.) cover the end-to-end paths.
func NewServer(cfg *config.Config, svc serviceAPI) *Server {
	s := &Server{
		config:        cfg,
		service:       svc,
		router:        chi.NewRouter(),
		fallback:      &fallbackTemplates{},
		assetVersions: buildAssetVersions(),
	}

	s.initOAuth()
	s.installMiddleware()
	s.loadTemplates()
	s.initIngestQueue(svc)
	s.installAssetFuncs()
	s.registerRoutes()

	return s
}

// installAssetFuncs registers the `asset` template function so
// every page can write `{{ asset "daisyui.min.css" }}` and get
// back "/static/daisyui.min.css?v=<sha>". The FuncMap is
// applied to both the embedded-template branch and the fallback
// Go-string templates so a single helper covers every
// page-rendering path.
//
// Behavior contract (pinned by TestInstallAssetFuncs_*):
//   - s.templates (when non-nil) gains the FuncMap. A nil
//     templates is left nil — the asset function won't be
//     available, but no panic; serveBasicHTML routes around
//     the missing template anyway.
//   - s.fallback.chat and s.fallback.documents are always
//     populated (parseFallback is never given an empty body).
//   - Unknown asset names resolve to the unversioned URL
//     "/static/<name>" — the static handler's permissive
//     fallback serves the same unversioned file. The typo-
//     tolerance is intentional: a missing-asset panic would
//     take down the page.
//   - The function is deterministic: two calls with the same
//     name return the same URL. assetVersions is keyed by name
//     and never mutates after NewServer returns.
func (s *Server) installAssetFuncs() {
	funcs := template.FuncMap{
		"asset": s.assetURL,
	}
	if s.templates != nil {
		s.templates = s.templates.Funcs(funcs)
	}
	s.fallback.chat = parseFallback("chat.html", chatFallbackBody, funcs)
	s.fallback.documents = parseFallback("documents.html", documentsFallbackBody, funcs)
}

// loadTemplates parses the page templates into s.templates.
//
// Precedence:
//
//  1. cfg.Paths.TemplatesDir, if set (operator override via
//     yaml / env TEMPLATES_DIR; intended for shipping a custom
//     template set without rebuilding the binary).
//  2. The embedded templatesFS (compile-time embed; default
//     for the published Docker image, which has no templates/
//     on disk).
//
// Both branches parse into the same *template.Template, so
// the rest of the package doesn't care which path served the
// templates. A failure in the operator-supplied branch logs
// and falls back to the embedded set; a failure in the
// embedded branch logs and leaves s.templates as a bare
// "base" template so the rest of the package can still
// run (every page goes through serveBasicHTML → fallback
// rendering in that case).
//
// Asset FuncMap trick: the embedded templates use
// {{ asset "..." }} calls but the original ParseFS doesn't
// carry our FuncMap. We re-parse via
// template.New("base").Funcs(...) which bakes the FuncMap
// into the receiver at parse time. Without this re-parse
// html/template fails the parse on the missing function and
// the operator gets a page that renders only the navbar.
//
// The receiver name "base" is mostly cosmetic: ParseFS
// associates each file with its base name, and the per-page
// render path goes through ExecuteTemplate on each named
// sub-template, so the receiver name is just the fallback
// Execute target. "base" is fine.
//
// Header block: the shared `header` template block is
// registered (issue #85 follow-on) so embedded page
// templates can invoke it via {{ template "header" .Header }}.
// Without this the embedded templates/ (which is the default)
// would render with no navbar — only the fallback Go-string
// templates include the header today. Parse errors here are
// logged and ignored; the navbar missing is recoverable
// (a template that calls {{ template "header" }} simply
// no-ops if the block is missing).
//
// Behavior contract (pinned by TestLoadTemplates_*):
//   - cfg.Paths.TemplatesDir empty → embedded set parsed
//     (search.html, etc. present).
//   - cfg.Paths.TemplatesDir set to a directory with .html
//     files → those files win over the embedded set.
//   - cfg.Paths.TemplatesDir set to a missing/empty path →
//     log + fall back to embedded.
//   - Every embedded template's {{ asset ... }} call must
//     resolve; the re-parse is what makes that work.
//   - The shared "header" block is registered.
func (s *Server) loadTemplates() {
	// Load templates in this order of precedence:
	//   1. cfg.Paths.TemplatesDir, if set
	//   2. The embedded templatesFS
	var templates *template.Template
	var err error
	switch {
	case s.config.Paths.TemplatesDir != "":
		templates, err = template.ParseGlob(filepath.Join(s.config.Paths.TemplatesDir, "*.html"))
		if err != nil {
			log.Printf("web: failed to load templates from %s: %v; falling back to embedded templates", s.config.Paths.TemplatesDir, err)
			templates, err = template.ParseFS(templatesFS, "templates/*.html")
		}
	default:
		templates, err = template.ParseFS(templatesFS, "templates/*.html")
	}
	// If the templates carried `{{ asset "..." }}` calls
	// (every embedded template does; see the cache-busting
	// contract in asset_version.go), the parser needs the
	// `asset` function in its FuncMap before ParseFS runs.
	// The re-parse below with the FuncMap baked in succeeds
	// in that case; without it html/template fails the
	// parse on the missing function.
	{
		var source string
		if s.config.Paths.TemplatesDir != "" {
			source = s.config.Paths.TemplatesDir
		} else {
			source = "embedded (templates/)"
		}
		withFuncs, parseErr := template.New("base").Funcs(template.FuncMap{
			"asset": AssetURL,
		}).ParseFS(templatesFS, "templates/*.html")
		if parseErr != nil {
			log.Printf("web: failed to re-parse %s with asset FuncMap: %v", source, parseErr)
			// Keep the original parse — it may have partial
			// state that's still better than nothing, and the
			// operator's templates will fall back to bare
			// "base" only if the original parse failed too.
			if templates == nil {
				templates = template.New("base")
			}
		} else {
			templates = withFuncs
		}
	}
	// Register the shared `header` template block (issue #85
	// follow-on) so embedded page templates can invoke it via
	// {{ template "header" .Header }}.
	if templates != nil {
		if _, parseErr := templates.Parse(pageHeaderFallbackBody); parseErr != nil {
			log.Printf("web: failed to register header block on embedded templates: %v", parseErr)
		}
	}
	// The original ParseGlob / ParseFS path may have failed
	// with a "function 'asset' not defined" error — that
	// happens because the embedded templates use {{ asset ... }}
	// calls but ParseFS doesn't carry our FuncMap. The
	// re-parse above (with the FuncMap baked in) succeeds
	// in that case, so by this point templates is non-nil
	// and the bare "base" fallback is only reached if the
	// re-parse ALSO failed — a true operator error that
	// deserves the loud log.
	if err != nil && templates == nil {
		log.Printf("web: failed to load templates: %v", err)
		templates = template.New("base")
	}
	s.templates = templates
}

// initOAuth populates s.sessions and s.oauthHandlers when at
// least one OAuth provider is configured. With no providers
// configured (the historical single-user / bearer-token
// install) both fields stay nil so the existing auth path
// keeps working.
//
// When newOAuthHandlers returns an error (e.g. a provider
// with missing required fields), s.oauthHandlers stays nil
// but s.sessions is still constructed — the server starts
// without session auth so other endpoints (bearer-token
// /api/*) keep working. A loud log line names the failure.
//
// Cookie-name default: when Auth.CookieName is empty the
// well-known "ragabast_session" is applied to oauthHandlers.
// Writer (callback handlers) and reader (authMiddleware) must
// agree on the name — a mismatch would silently break login.
// Without the default, setSessionCookie would write "" and
// readSessionCookie would never match.
//
// Session-TTL default: when Auth.SessionTTL is zero, the
// store gets 12 hours. The 12h cap is a soft safety net
// for the historical "sessions live forever" behavior that
// bit operators who never set the knob.
//
// Behavior contract (pinned by TestInitOAuth_*):
//   - No providers → s.sessions and s.oauthHandlers both nil.
//   - Provider(s) + default cookie name →
//     s.oauthHandlers.cookieName == "ragabast_session".
//   - Provider(s) + custom cookie name → that name wins.
//   - Provider(s) + Auth.SessionTTL == 0 → store is non-nil
//     and a session can be minted and retrieved.
//   - Provider with missing required fields (e.g. github
//     without ClientSecret) → s.oauthHandlers nil,
//     s.sessions still constructed.
func (s *Server) initOAuth() {
	if len(s.config.Auth.Providers) == 0 {
		return
	}

	// sessionTTL zero → sessions live forever
	// (in-memory, until restart). Default to 12h
	// when the operator hasn't set one.
	ttl := s.config.Auth.SessionTTL
	if ttl == 0 {
		ttl = 12 * time.Hour
	}
	s.sessions = newSessionStore(ttl)

	// Cookie name: the operator can override via
	// cfg.Auth.CookieName (or AUTH_COOKIE_NAME env);
	// empty means "use the default" so writer and
	// reader stay in sync. Without this default,
	// setSessionCookie would set the cookie as ""
	// (which browsers reject silently) and
	// readSessionCookie would never match.
	cookieName := s.config.Auth.CookieName
	if cookieName == "" {
		cookieName = "ragabast_session"
	}

	serverBase := deriveServerBase(s.config)
	oh, err := newOAuthHandlers(s.config, serverBase, s.sessions)
	if err != nil {
		log.Printf("web: failed to initialize OAuth providers: %v; starting without session auth", err)
		return
	}
	// Override the cookie name on the oauthHandlers
	// struct so callback handlers write the same
	// cookie the middleware reads.
	oh.cookieName = cookieName
	s.oauthHandlers = oh
}

// middlewareTimeout is the per-request budget the chi
// middleware.Timeout enforces. The chi middleware cancels
// the handler context but NOT the underlying connection;
// only the http.Server timeouts do that. 60s is a balance:
// short enough that a runaway prompt can't pin a worker
// forever, long enough that legitimate LLM-backed handlers
// (which can take tens of seconds for large-context
// requests) finish.
//
// Pinned by TestInstallMiddleware_TimeoutReturns504WhenHandlerSleeps
// to catch regressions that would silently drop or extend
// the timeout.
const middlewareTimeout = 60 * time.Second

// installMiddleware assembles the chi router's middleware
// chain. The ordering is significant: each comment below
// names the reason for its position. NewServer must call
// initOAuth BEFORE installMiddleware so the auth middleware
// can read s.sessions and s.oauthHandlers.cookieName.
//
// Chain order (top to bottom = first to last to see the
// request):
//
//  1. securityHeadersMiddleware — defense headers land on
//     every response, including errors from middleware
//     deeper in the chain.
//  2. requestIDMiddleware — every downstream middleware,
//     log line, and handler can read the ID via
//     RequestIDFromContext. Placed before the access logger
//     and recoverer so panic logs and access lines are
//     correlated; placed after securityHeaders to keep the
//     defense-header layer dependency-free.
//  3. redactAccessLogMiddleware — mutates r.URL.RawQuery
//     so chi's middleware.Logger sees redacted values for
//     known sensitive keys (query/messages/text). MUST
//     run before middleware.Logger.
//  4. middleware.Logger — access log.
//  5. middleware.Recoverer — panic-to-500.
//  6. middleware.RealIP — rewrites r.RemoteAddr from
//     X-Forwarded-For / X-Real-IP.
//  7. clientIPMiddleware — extracts the host portion of
//     r.RemoteAddr and stashes it on the request context
//     for ClientIPFromContext.
//  8. maxBytesReaderMiddleware — caps request body.
//  9. middleware.Timeout — cancels the handler context
//     after middlewareTimeout. The chi middleware does
//     not close the connection; only the http.Server
//     timeouts (ReadTimeout/WriteTimeout/IdleTimeout) do
//     that.
//  10. corsMiddleware — only when cfg.Server.EnableCORS.
//     Runs before auth so OPTIONS preflight requests do
//     not require a bearer token (browsers do not send
//     credentials on preflight).
//  11. authMiddleware — runs last so the body-size limit,
//     security headers, CORS preflight, and request
//     logging all apply to auth-failed requests too.
//     With EffectiveAuthTokens empty the middleware is a
//     no-op so the single-user local install keeps
//     working.
//  12. csrfMiddleware — runs after auth so a Bearer-auth
//     POST (which cannot be made cross-origin by a
//     browser) skips the CSRF check entirely. Runs before
//     the rate limiter so a CSRF-failed flood still
//     consumes bucket tokens — defense-in-depth that
//     keeps a malicious page from probing token guesses
//     with no rate-limit cost.
//  13. rateLimiter.llmPathMiddleware — path-aware; only
//     throttles requests matching llmPathPrefixes (see
//     rate_limit.go) and passes every other request
//     through. One middleware covers both the
//     Huma-mounted LLM routes and the form-mounted
//     LLM routes.
//
// Behavior contract (pinned by TestInstallMiddleware_*):
//   - Security headers present on every response.
//   - X-Request-Id generated per request, distinct between
//     two consecutive requests.
//   - CORS skipped when EnableCORS=false; applied
//     (origin echoed) for allow-listed origins; rejected
//     (not echoed) for non-allow-listed origins.
//   - Auth gate: 401 on missing token when AuthToken set;
//     200 on Bearer with the right token; pass-through
//     when no tokens configured.
//   - Timeout middleware in effect with middlewareTimeout
//     (60s) as the budget.
func (s *Server) installMiddleware() {
	// securityHeadersMiddleware runs first so the defense
	// headers land on every response — including error
	// responses from middleware deeper in the chain.
	s.router.Use(securityHeadersMiddleware)
	// requestIDMiddleware runs second so every downstream
	// middleware, log line, and handler can read the ID via
	// RequestIDFromContext. Placing it before the access
	// logger and recoverer means panic logs and access lines
	// are correlated; placing it after securityHeaders
	// keeps the defense-header layer dependency-free.
	s.router.Use(requestIDMiddleware())
	// redactAccessLogMiddleware MUST run before
	// middleware.Logger so chi sees the rewritten URL when
	// it formats the access line. The middleware mutates
	// r.URL.RawQuery in place (replacing values for known
	// sensitive keys with "[REDACTED]") so the operator
	// query/messages/text fields never land in the log.
	s.router.Use(redactAccessLogMiddleware)
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.RealIP)
	// clientIPMiddleware runs immediately after chi's RealIP
	// so r.RemoteAddr has already been rewritten from
	// X-Forwarded-For / X-Real-IP headers. The middleware
	// extracts the host portion and stores it on the
	// request context; downstream callers (audit log,
	// rate-limit) read it via ClientIPFromContext.
	s.router.Use(clientIPMiddleware())
	s.router.Use(maxBytesReaderMiddleware)
	s.router.Use(middleware.Timeout(middlewareTimeout))

	// CORS runs before auth so OPTIONS preflight requests do
	// not require a bearer token (browsers do not send
	// credentials on preflight). The new allow-list middleware
	// replaces the previous wildcard-or-nothing behavior.
	if s.config.Server.EnableCORS {
		s.router.Use(corsMiddleware(s.config.Server.CORSOrigins))
	}

	// Auth runs last so the body-size limit, security
	// headers, CORS preflight, and request logging all apply
	// to auth-failed requests too. With auth_token empty
	// (the default) the middleware is a no-op so the
	// single-user local install keeps working.
	// EffectiveAuthTokens merges the singular AuthToken
	// (backward-compatible shortcut) with the modern
	// AuthTokens list, deduping by Value. installMiddleware
	// is the only call site for authMiddleware — keep it
	// that way so the security boundary is easy to audit.
	cookieName := ""
	if s.oauthHandlers != nil {
		cookieName = s.oauthHandlers.cookieName
	}
	s.router.Use(authMiddleware(s.config.Server.EffectiveAuthTokens(), s.sessions, cookieName))

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
	s.router.Use(csrfMiddleware(s.config.Server.EffectiveAuthTokens()))

	// Rate-limit middleware for the LLM-backed endpoints. The
	// middleware is path-aware (see rate_limit.go) and a no-op
	// for any non-LLM URL prefix; it is installed at the
	// chain level so it covers both the Huma-mounted
	// endpoints and the form-mounted endpoints without
	// wrapping each route individually.
	rateLimiter := newLLMRateLimiter(s.config.Server.RateLimitPerMinute, s.config.Server.RateLimitBurst)
	s.router.Use(rateLimiter.llmPathMiddleware)
}

// initIngestQueue populates s.ingestQueue when
// cfg.Server.AsyncIngestQueueDir is set. The queue is the
// source of truth for /api/ingest/async: submissions land
// here, a bounded worker pool drains it, every state
// transition is persisted to <AsyncIngestQueueDir>. A
// restart re-enqueues any pending/processing jobs.
//
// Empty AsyncIngestQueueDir disables async ingest — the
// /api/ingest/async endpoint returns 503 in that case. The
// synchronous POST /api/ingest path is unaffected.
//
// The queue's IngestDocument adapter wraps the serviceAPI
// (passed in as svc); we hand it svc so workers can call
// IngestDocument without reaching back into the
// web-package's internals.
//
// Package-level globalIngestQueue: set whenever a queue is
// created, and reset to nil BEFORE the no-queue branch so
// a previous test that enabled the queue doesn't leak its
// state into the next test's /api/health/full response.
// The reset is the same defensive line the original
// NewServer carried.
//
// Behavior contract (pinned by TestInitIngestQueue_*):
//   - Empty AsyncIngestQueueDir → s.ingestQueue nil,
//     globalIngestQueue nil.
//   - Writable AsyncIngestQueueDir → s.ingestQueue non-nil,
//     globalIngestQueue == s.ingestQueue, workers started.
//   - jobs.New error (e.g. dir can't be created) →
//     s.ingestQueue nil, server doesn't panic.
//   - Reset path: a non-nil globalIngestQueue at entry is
//     cleared before deciding whether to create a new queue.
func (s *Server) initIngestQueue(svc serviceAPI) {
	// Reset the package-scope queue reference so a server
	// constructed without async ingest sees ingest_queue.enabled
	// = false in /api/health/full. Without this reset, a
	// previous test that enabled the queue would leak its
	// state into the next test's /api/health/full response.
	globalIngestQueue = nil

	if s.config.Server.AsyncIngestQueueDir == "" {
		s.ingestQueue = nil
		return
	}

	q, qerr := jobs.New(
		s.config.Server.AsyncIngestQueueDir,
		s.config.Server.MaxIngestDocumentBytes,
		s.config.Server.AsyncIngestWorkers,
	)
	if qerr != nil {
		log.Printf("web: failed to create ingest queue at %s: %v; async ingest disabled", s.config.Server.AsyncIngestQueueDir, qerr)
		s.ingestQueue = nil
		return
	}
	s.ingestQueue = q
	// Stash the queue handle at package scope so the
	// /api/health/full handler can read counters
	// without crossing the serviceAPI boundary twice.
	// Set before Start so the health handler sees the
	// post-Start state once the cleanup loop is up.
	globalIngestQueue = q
	q.Start(jobsServiceAdapter{svc: svc}, auditFunc(log.Printf))
	// Background cleanup sweep. Operates on the same
	// audit hook as the worker pool so the operator's
	// log stream is unified. TTL=0 on both knobs
	// disables cleanup entirely; StartCleanup is a
	// no-op in that case.
	q.StartCleanup(
		s.config.Server.AsyncIngestCleanupInterval,
		s.config.Server.AsyncIngestCompletedJobTTL,
		s.config.Server.AsyncIngestFailedJobTTL,
		auditFunc(log.Printf),
	)
	log.Printf("web: async ingest queue started at %s (workers=%d, max_document_bytes=%d, cleanup_interval=%s, completed_ttl=%s, failed_ttl=%s)",
		q.Dir(), q.Workers(), q.MaxBytes(),
		s.config.Server.AsyncIngestCleanupInterval,
		s.config.Server.AsyncIngestCompletedJobTTL,
		s.config.Server.AsyncIngestFailedJobTTL)
}

// registerAuthRoutes wires the OAuth handlers into the
// chi router. The routes are always registered so
// /auth/login and /auth/me return predictable responses
// whether OAuth is configured or not. The per-provider
// handlers short-circuit to a 503 when oauthHandlers is
// nil (the historical single-user / bearer-token
// install) so an operator hitting the path sees the
// missing-config signal in the log.
func (s *Server) registerAuthRoutes() {
	s.router.Get("/auth/login", s.handleAuthLogin)
	s.router.Get("/auth/{provider}/login", s.handleAuthProviderLogin)
	s.router.Get("/auth/{provider}/callback", s.handleAuthProviderCallback)
	s.router.Post("/auth/logout", s.handleAuthLogout)
	s.router.Get("/auth/me", s.handleMe)
}

// handleAuthLogin dispatches to the OAuth handler when
// configured, 503 otherwise.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.oauthHandlers == nil {
		http.Error(w, "OAuth providers are not configured", http.StatusServiceUnavailable)

		return
	}
	s.oauthHandlers.handleLogin(w, r)
}

// handleAuthProviderLogin dispatches to the per-provider
// handler when configured.
func (s *Server) handleAuthProviderLogin(w http.ResponseWriter, r *http.Request) {
	if s.oauthHandlers == nil {
		http.Error(w, "OAuth providers are not configured", http.StatusServiceUnavailable)

		return
	}
	s.oauthHandlers.handleProviderLogin(w, r, chi.URLParam(r, "provider"))
}

// handleAuthProviderCallback dispatches to the
// per-provider handler when configured.
func (s *Server) handleAuthProviderCallback(w http.ResponseWriter, r *http.Request) {
	if s.oauthHandlers == nil {
		http.Error(w, "OAuth providers are not configured", http.StatusServiceUnavailable)

		return
	}
	s.oauthHandlers.handleProviderCallback(w, r, chi.URLParam(r, "provider"))
}

// handleAuthLogout dispatches to the OAuth handler when
// configured. When auth is unconfigured, respond 204 so
// the UI's "Sign out" button is a safe no-op rather than
// an error.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.oauthHandlers == nil {
		w.WriteHeader(http.StatusNoContent)

		return
	}
	s.oauthHandlers.handleLogout(w, r)
}

// handleMe returns the current session's user identity as
// JSON for the UI's "signed in as" widget. 200 with
// anonymous=true when no session is present (so the UI can
// show "Sign in" without branching on status codes); 401
// when auth is configured and no session resolves — keeps
// the response consistent with the rest of the API.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if s.oauthHandlers == nil || s.sessions == nil {
		_, _ = w.Write([]byte(`{"authenticated":false}`))

		return
	}

	sess, ok := s.sessions.Get(readSessionCookie(r, s.resolveSessionCookieName()))
	if !ok {
		_, _ = w.Write([]byte(`{"authenticated":false}`))

		return
	}

	u := userFromSession(sess)
	role := u.Role
	if role == "" {
		role = "user"
	}
	body := map[string]any{
		"authenticated": true,
		"subject":       u.Subject,
		"username":      u.Username,
		"email":         u.Email,
		"name":          u.Name,
		"provider":      u.ProviderName,
		"role":          role,
	}
	enc := json.NewEncoder(w)
	_ = enc.Encode(body)
}

// deriveServerBase is the externally-reachable origin
// (scheme + host [+ port]) the IdP redirects back to.
// The result is used as the prefix for every OAuth
// callback URL: `<derived>/auth/<provider-name>/callback`.
//
// Resolution order:
//
//  1. cfg.Server.PublicURL — operator-provided override
//     for installs behind a reverse proxy (Traefik, nginx,
//     Caddy, an L7 cloud LB) where the bind address is a
//     private hostname but the public URL is something
//     else entirely. Empty by default; setting it
//     preserves everything (scheme, host, port, optional
//     subpath) so the URL the IdP gets matches what the
//     operator registered at the provider.
//  2. http://<address>:<port> — historical fallback when
//     PublicURL is empty. Strips a port from Address if
//     it already carries one, then re-appends the
//     configured Port. Default scheme is http because
//     ragabast doesn't terminate TLS itself; operators
//     behind a TLS proxy should set PublicURL instead.
//
// A trailing slash on PublicURL is trimmed so the
// concatenation in OAuthProvider.RedirectURL never
// produces a double slash.
func deriveServerBase(cfg *config.Config) string {
	if pub := strings.TrimSpace(cfg.Server.PublicURL); pub != "" {
		return strings.TrimRight(pub, "/")
	}

	scheme := "http"
	addr := cfg.Server.Address
	port := cfg.Server.Port
	if port == 0 {
		port = 8080
	}
	if addr == "" {
		addr = "localhost"
	}

	// Strip a port from Address if it already carries
	// one; we'll re-append the configured Port.
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}

	return scheme + "://" + net.JoinHostPort(addr, strconv.Itoa(port))
}

// resolveSessionCookieName returns the session-cookie name
// the server actually uses, applying the default
// ("ragabast_session") when the operator hasn't set one.
// Centralizing the default keeps the cookie writer (in
// the OAuth callback) and reader (authMiddleware,
// handleMe) in sync — a mismatch would silently break
// login with no signal in the logs.
func (s *Server) resolveSessionCookieName() string {
	if s.oauthHandlers != nil && s.oauthHandlers.cookieName != "" {
		return s.oauthHandlers.cookieName
	}
	if s.config.Auth.CookieName != "" {
		return s.config.Auth.CookieName
	}

	return "ragabast_session"
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
	s.router.Get("/search", s.handleSearchPage)
	s.router.Post("/search", s.handleSearchSubmit)
	s.router.Get("/documents", s.handleDocumentsPage)
	s.router.Post("/documents/{document_id}/delete", s.handleDocumentDelete)

	s.router.Get("/static/*", s.handleStatic)

	// OAuth login / callback / logout / me. Registered
	// here rather than via Huma because they don't speak
	// OpenAPI; they're browser-flow endpoints. /auth/* is
	// also added to isPublicRoute so a logged-out browser
	// can hit /auth/login without being redirected to
	// itself.
	s.registerAuthRoutes()

	// MCP HTTP transport — opt-in. When
	// server.mcp_http_enabled is true, mount the streamable
	// HTTP handler at /mcp on the same listen port. Auth
	// is inherited from the global authMiddleware — the
	// existing server.auth_tokens list applies. When auth
	// is unconfigured, /mcp is open (matches the rest of
	// the API surface). Opt-in by default so existing
	// operators aren't surprised by a new endpoint.
	if s.config.Server.MCPHTTPEnabled {
		s.router.Handle("/mcp", mcpHTTPHandler(s.service))
	}
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
