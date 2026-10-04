package web

// Phase 9 of plans/ux-overhaul.md (docs CSP follow-on).
//
// The huma v2.34.1 docs template emits:
//
//   <body style="height: 100vh;">
//     <elements-api apiDescriptionUrl="…/openapi.yaml" ... />
//   </body>
//
// The <body> inline style is a fixed string huma ships
// verbatim. The <elements-api> tag is a custom element
// (Stoplight Elements) that generates dynamic inline
// <style> blocks in its shadow DOM at runtime. Both are
// blocked by cspWithDocs' pre-Phase-9 style-src of
// 'self' https://unpkg.com (no 'unsafe-inline'). The
// visible symptom: the Stoplight SVG icons (expand,
// collapse, search, copy-as-cURL, lock, etc.) render at
// browser default sizes because the inline styles that
// set their width/height/color are silently dropped. The
// <body> collapses to default height.
//
// Phase 9 adds 'unsafe-inline' to cspWithDocs' style-src.
// The risk is bounded:
//   - script-src stays 'self' https://unpkg.com — no
//     remote code execution becomes available.
//   - 'unsafe-inline' for styles can enable CSS
//     exfiltration via background-image or clickjacking
//     tricks, but cannot execute scripts. /docs does
//     not render user input (Huma serves a fixed
//     template populated with the server-generated
//     OpenAPI doc), so the attack surface is small.
//
// This file pins the new contract.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestSecurityHeaders_DocsAllowsInlineStyles pins the
// Phase 9 fix: the /docs CSP must allow inline styles
// (`'unsafe-inline'` in style-src) so huma's
// `<body style="height: 100vh;">` and the Stoplight
// Elements web component's runtime-generated inline
// `<style>` blocks both render. Without this, the
// Stoplight SVG icons break (CSS not applied, browser
// default sizes) and the body collapses to content
// height.
func TestSecurityHeaders_DocsAllowsInlineStyles(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/docs", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	csp := w.Header().Get("Content-Security-Policy")
	require.Contains(t, csp, "style-src",
		"/docs CSP must declare style-src so the directive is reviewable")
	require.Contains(t, csp, "style-src 'self' https://unpkg.com 'unsafe-inline'",
		"/docs CSP style-src must include 'unsafe-inline' so huma's <body style=\"height: 100vh;\"> and Stoplight's runtime inline <style> blocks render (Phase 9 docs CSP follow-on)")
}

// TestSecurityHeaders_DocsBodyKeepsInlineStyleHeight pins
// the user-visible symptom: with the Phase 9 fix, the
// huma-emitted `<body style="height: 100vh;">` is in the
// HTML, and the CSP allows it. This test catches a
// regression in either the template (if huma changes)
// or the CSP (if someone re-tightens the style-src).
func TestSecurityHeaders_DocsBodyKeepsInlineStyleHeight(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/docs", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	require.Contains(t, body, `<body style="height: 100vh;">`,
		"huma's docs template must still emit <body style=\"height: 100vh;\"> — a future huma upgrade that drops the inline style would let us tighten the CSP back down to no-'unsafe-inline' (revisit then)")
}

// TestSecurityHeaders_AppRoutesStillRejectInlineStyles
// pins the negative half of the Phase 9 contract: the
// app-route strict CSP must still reject 'unsafe-inline'
// for styles. Phase 9 only relaxes the /docs CSP; the
// chat / search / documents / API surfaces keep the
// strict policy.
func TestSecurityHeaders_AppRoutesStillRejectInlineStyles(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	csp := w.Header().Get("Content-Security-Policy")

	require.NotContains(t, csp, "'unsafe-inline'",
		"app-route CSP must not allow 'unsafe-inline' (Phase 9 only relaxes the /docs CSP, never the strict CSP)")
}
