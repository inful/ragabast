package web

import (
	"net/http/httptest"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestUx_SkeletonClass pins the Phase 1.3 contract from
// plans/ux-overhaul.md: the daisyUI skeleton class is
// established as the canonical loading-state placeholder.
//
// Phase 1 introduces the class usage so the CSS is
// exercised and grep-able; Phase 4 wires it to actual
// loading flows (document fetch, search in-flight,
// chat-log hydration). The contract at this stage is:
// at least one element on a rendered full page carries
// class="skeleton".
//
// Phase 2.1 of the SPEC replaced the chat landing page's
// skeleton placeholders with clickable suggested prompts
// (the spec calls the suggested-prompts card "the
// largest new-user onboarding win"). The skeleton class
// is wired to actual loading states in Phase 4 (documents
// page filter bar via hx-indicator). We pin the
// documents page for the skeleton class — it's the
// canonical "loading state" surface post-Phase 4.
func TestUx_SkeletonClass(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = "" // force fallback renderer
	// The documents page renders the filter input +
	// the hx-indicator skeleton block only when
	// there's at least one document. Seed the fake
	// service with a single document so the page
	// is in the populated state.
	s := NewServer(cfg, &fakeService{documents: []models.DocumentInfo{
		{ID: "doc-1", UID: "doc-1", Title: "Doc 1"},
	}})

	// The documents page is the canonical loading
	// state surface. The hx-indicator attribute
	// on the filter input (Phase 4.2) names a
	// target element that daisyUI's htmx
	// integration auto-toggles with .htmx-request;
	// the skeleton class lives on a child of that
	// target so the placeholder shows while the
	// request is in flight.
	req := httptest.NewRequestWithContext(t.Context(),
		"GET", "/documents", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code,
		"GET /documents must return 200")
	body := w.Body.String()

	// The base class "skeleton" must appear at
	// least once. The daisyUI class is the
	// contract; the element type, sizing utility,
	// and container layout are all designer
	// choices that the test intentionally does
	// not pin.
	require.Contains(t, body, "skeleton",
		"the documents page must render at least one element with class=\"skeleton\" so the loading-state placeholder pattern is established (Phase 4 wires the skeleton to the filter-bar hx-indicator)")
}
