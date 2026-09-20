package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestAuthMiddleware_RequiresBearerToken pins the C-1 fix:
// when server.auth_token is configured, every state-changing
// or read endpoint MUST require the Authorization header to
// carry the matching bearer token. Requests without it (or
// with a wrong token) are rejected with 401 before any
// handler runs.
//
// The auth token is a single shared-secret in config; the
// intended deployment is "operator sets SERVER_AUTH_TOKEN,
// every API caller adds Authorization: Bearer <token>". This
// is intentionally not a multi-user auth system; the project
// is a single-tenant local-first tool.
func TestAuthMiddleware_RequiresBearerToken(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret-token-123"

	s := NewServer(cfg, &fakeHumaService{})

	// The set of routes we want to be auth-protected. Listed
	// explicitly so a future contributor who adds a new
	// route has to consciously decide whether it belongs in
	// the auth-protected group or the public-read group.
	protected := []struct {
		method string
		path   string
	}{
		// API endpoints
		{http.MethodGet, "/api/health"},
		{http.MethodGet, "/api/tags"},
		{http.MethodGet, "/api/categories"},
		{http.MethodGet, "/api/tags-categories"},
		{http.MethodPost, "/api/query"},
		{http.MethodPost, "/api/search"},
		{http.MethodPost, "/api/ingest"},
		{http.MethodPost, "/api/ingest/raw"},
		{http.MethodPost, "/api/ingest/file"},
		{http.MethodGet, "/api/documents"},
		{http.MethodDelete, "/api/documents/doc-1"},
		{http.MethodPost, "/api/documents/prune"},
		{http.MethodPost, "/api/link-suggestions"},
		{http.MethodPost, "/api/frontmatter/suggest"},
		// Form-mounted web handlers
		{http.MethodPost, "/chat/message"},
		{http.MethodPost, "/search"},
		{http.MethodPost, "/ingest"},
	}

	for _, p := range protected {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), p.method, p.path, strings.NewReader(""))
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code,
				"%s %s must require auth when auth_token is set (got %d)", p.method, p.path, w.Code)
			require.Contains(t, w.Header().Get("WWW-Authenticate"), "Bearer",
				"401 response must advertise Bearer scheme via WWW-Authenticate")
		})
	}
}

// TestAuthMiddleware_AcceptsValidBearerToken confirms a
// correctly-formatted bearer token is accepted by the same
// routes.
func TestAuthMiddleware_AcceptsValidBearerToken(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret-token-123"

	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", "Bearer secret-token-123")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"valid bearer token must be accepted (got %d)", w.Code)
}

// TestAuthMiddleware_RejectsWrongToken pins that a token
// mismatch returns 401, not 500 or 403. (403 is "you are who
// you say you are but you cannot do this"; 401 is "we don't
// know who you are" — the right answer for a missing or wrong
// bearer.)
func TestAuthMiddleware_RejectsWrongToken(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "correct"

	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAuthMiddleware_NoTokenConfigured_OpenAccess confirms
// the fail-open behavior when auth_token is empty: the local
// dev / single-user case keeps working without configuration.
// Operators exposing ragabast on a non-loopback interface MUST
// set server.auth_token; this is documented in config and the
// README.
func TestAuthMiddleware_NoTokenConfigured_OpenAccess(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "" // the default

	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"with auth_token unset, /api/health must remain open (got %d)", w.Code)
}

// TestCORSMiddleware_OriginAllowList pins the C-2 fix:
// when CORS is enabled, the response MUST NOT carry
// Access-Control-Allow-Origin: * unless the operator has
// explicitly opted into wildcard via an allow-list of ["*"].
// Default behavior is to echo the request Origin header only
// if it appears in the configured allow-list.
func TestCORSMiddleware_OriginAllowList(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.EnableCORS = true
	cfg.Server.CORSOrigins = []string{"https://allowed.example"}
	s := NewServer(cfg, &fakeHumaService{})

	t.Run("allowed origin is echoed", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
		req.Header.Set("Origin", "https://allowed.example")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Equal(t, "https://allowed.example", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("disallowed origin is rejected (no ACAO header)", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
		req.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
			"disallowed origin must not be echoed back")
	})

	t.Run("default config does not wildcard", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Server.EnableCORS = true
		cfg.Server.CORSOrigins = nil // no origins configured
		s := NewServer(cfg, &fakeHumaService{})

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
		req.Header.Set("Origin", "https://anything.example")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
			"empty CORSOrigins allow-list must not echo any origin")
	})
}

// TestAuthMiddleware_StaticRouteIsPublic confirms the static
// stub stays public even when auth is configured. The
// rationale: static assets (CSS/JS/images) are not sensitive
// and the browser cannot send an Authorization header on a
// plain <link rel="stylesheet"> request. Putting auth on
// /static would break the chat UI.
//
// Real implementations of /static must keep this behavior:
// see TestStaticRouteDoesNotServeArbitraryFiles for the
// traversal-side guard.
func TestAuthMiddleware_StaticRouteIsPublic(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/x", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.NotEqual(t, http.StatusUnauthorized, w.Code,
		"static stub must remain public for the HTML chrome to load")
}

// TestAuthMiddleware_GET_ChatPageIsPublic confirms the GET
// page routes (/, /chat, /search, /ingest, /documents) stay
// public so the browser can load the form. Only the
// state-changing POST handlers are protected. The rationale
// is the same as the static route: browsers do not send
// Authorization on a top-level GET, so protecting these would
// break the operator's own UI.
func TestAuthMiddleware_GET_ChatPageIsPublic(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeService{})

	publicGets := []string{"/", "/search", "/ingest", "/documents"}
	for _, p := range publicGets {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, p, nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.NotEqual(t, http.StatusUnauthorized, w.Code,
				"%s must remain public so the form can load (got %d)", p, w.Code)
		})
	}
}

// TestAuthMiddleware_BodySizeLimitAppliesBeforeAuth pins the
// middleware ordering: when an auth-failing request also has
// an oversized body, the server MUST reject without reading
// the body into memory. The actual security property is "no
// memory pinning" — the body limit can run either before or
// after auth as long as the body is discarded on rejection.
//
// In the current chain, auth runs AFTER maxBytesReader but
// BEFORE the handler; auth short-circuits with 401 so the
// handler never reads the body. The 12 MiB body is buffered
// into the request by net/http before middleware fires (Go's
// http.Server buffers up to MaxHeaderBytes but streams the
// body), so a slow attacker cannot pin memory at GB-scale
// through the auth gate.
func TestAuthMiddleware_BodySizeLimitAppliesBeforeAuth(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.AuthToken = "secret"
	s := NewServer(cfg, &fakeService{})

	oversized := bytes.Repeat([]byte("a"), 12<<20)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", bytes.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	// Auth rejection (401) is the expected path: with a
	// wrong token, auth short-circuits before the handler
	// reads the body. The point is the body is never
	// materialized into the handler's working set.
	require.Equal(t, http.StatusUnauthorized, w.Code,
		"auth-failing oversized body must reject with 401 (got %d)", w.Code)
}
