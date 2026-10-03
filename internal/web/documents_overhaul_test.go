package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// documentsOverhaulTestServer builds the standard
// documents-test Server fixture for the Phase 4 tests.
// Uses the Service fake (not HumaService) because
// the documents page handler calls ListDocumentsPaged
// / DeleteDocument on the Service interface.
func documentsOverhaulTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
	s.templates = nil
	return s
}

// documentsOverhaulTestData builds a single-row
// documentsFallbackData fixture for the Phase 4 tests.
// Returns the populated data + the expected field
// values so each test can assert on the data it
// rendered with (rather than re-deriving values).
type documentsOverhaulTestData struct {
	Data documentsFallbackData
}

func newDocumentsTestData() documentsOverhaulTestData {
	return documentsOverhaulTestData{
		Data: documentsFallbackData{
			Title: "Ingested Documents",
			Header: pageHeaderData{
				AuthEnabled: false, SignedIn: false, ShowSignIn: false,
			},
			Total:        1,
			StartShowing: 1,
			EndShowing:   1,
			Limit:        25,
			PrevOffset:   -1,
			NextOffset:   -1,
			Documents: []documentsFallbackRow{
				{
					ID:           "doc-1",
					DisplayLabel: "Doc One",
					Tags:         []string{"alpha", "beta"},
					Category:     "Reference",
					Chunks:       3,
					CsrfToken:    "csrf-test",
				},
			},
		},
	}
}

// TestDocuments_EmptyStateHasCTA pins the Phase 4.1
// contract: the empty-state alert renders a primary
// CTA link to the ingest guide. The pre-Phase-4.1
// design told operators to POST /api/ingest but
// offered no in-page next step. Phase 4.1 adds a
// "read the ingest guide" link to the OpenAPI docs
// so the operator has a documented next step.
func TestDocuments_EmptyStateHasCTA(t *testing.T) {
	s := documentsOverhaulTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/documents", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The empty-state alert must render.
	assert.Contains(t, body, "Nothing here yet",
		"empty corpus must surface the Phase 6.1 copy (Nothing here yet.)")

	// The CTA link must point at the ingest guide.
	// Per the SPEC: <a href="/docs#/operations/ingest"
	// class="link link-primary">read the ingest guide →</a>.
	assert.Contains(t, body, `href="/docs#/operations/ingest"`,
		"empty state must include a CTA link to the ingest guide (Phase 4.1 contract)")
}

// TestDocuments_FilterBarPresent pins the Phase 4.2
// contract: the populated documents page renders a
// search input at the top of the table for
// client-side filtering. The pre-Phase-4.2 design
// had no way to filter the visible rows; operators
// had to scroll through the entire table to find a
// specific document. The Phase 4.2 filter is
// client-side (CSS :has() + [hidden] toggles) so it
// works without a server round-trip.
func TestDocuments_FilterBarPresent(t *testing.T) {
	s := documentsOverhaulTestServer(t)

	td := newDocumentsTestData()
	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, td.Data))
	body := buf.String()

	// The filter input is a <input type="search"
	// name="q"> above the table. Pin the type +
	// name + a placeholder so the contract is
	// stable.
	assert.Regexp(t, `<input[^>]*type="search"[^>]*name="q"`, body,
		"the documents page must render a client-side filter input above the table (Phase 4.2 contract)")

	// The filter input should carry a placeholder
	// that describes what it filters. We don't pin
	// the exact wording (design call), but the
	// placeholder must be present so the
	// discoverability is clear.
	assert.Regexp(t, `<input[^>]*type="search"[^>]*placeholder=`, body,
		"the filter input must carry a placeholder describing what it filters (Phase 4.2 contract)")
}

// TestDocuments_ClickableTag pins the Phase 4.3
// contract: tag chips in the documents table render
// as <a> elements (clickable) that link to
// /search?tag=foo with the tag pre-filled. The
// pre-Phase-4.3 design had static <span> tags —
// clicking did nothing, and the operator had to
// copy-paste the tag into the search form.
func TestDocuments_ClickableTag(t *testing.T) {
	s := documentsOverhaulTestServer(t)

	td := newDocumentsTestData()
	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, td.Data))
	body := buf.String()

	// The tag "alpha" must render as an <a> that
	// links to /search?tag=alpha. We accept the
	// href / class attributes in any order (HTML5
	// allows either).
	assert.Regexp(t, `<a[^>]*?href="/search\?tag=alpha"[\s\S]*?>alpha</a>`, body,
		"tag 'alpha' must render as a clickable <a> linking to /search?tag=alpha (Phase 4.3 contract)")

	// The tag "beta" must also be clickable (the
	// second tag in the fixture).
	assert.Regexp(t, `<a[^>]*?href="/search\?tag=beta"[\s\S]*?>beta</a>`, body,
		"tag 'beta' must render as a clickable <a> linking to /search?tag=beta (Phase 4.3 contract)")

	// The tag anchors use daisyUI's badge class
	// (was the static <span class="badge"> in the
	// pre-Phase-4.3 design; Phase 4.3 keeps the
	// badge styling and adds the anchor wrapper).
	// We match the <a> tag with both href and
	// class attributes present, in any order.
	assert.Regexp(t, `<a[^>]*?(?:href="/search\?tag=alpha"[\s\S]*?class="[^"]*\bbadge\b|class="[^"]*\bbadge\b[^"]*"[\s\S]*?href="/search\?tag=alpha")`, body,
		"the clickable tag chips must use daisyUI's badge class (Phase 4.3 contract)")
}

// TestDocuments_DeleteUsesModal pins the Phase 4.5
// contract: each row's delete action opens a
// confirmation <dialog class="modal"> rather than
// the inline <input type="checkbox" name="confirm">
// the pre-Phase-4.5 design used. The modal pattern
// is the daisyUI idiom (per SKILL.md) and uses
// chat.js's window.openModal / window.closeModal
// helpers (Phase 1.2 of plans/ux-overhaul.md) for
// the open/close wiring.
func TestDocuments_DeleteUsesModal(t *testing.T) {
	s := documentsOverhaulTestServer(t)

	td := newDocumentsTestData()
	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, td.Data))
	body := buf.String()

	// Each row must have a "Delete" button that
	// opens a modal (data-modal-open="delete-{ID}").
	assert.Regexp(t, `<button[^>]*data-modal-open="delete-doc-1"`, body,
		"each row's delete action must open a confirmation modal (Phase 4.5 contract)")

	// The inline <input type="checkbox" name="confirm">
	// pattern is gone — the modal is the new
	// confirmation surface.
	assert.NotRegexp(t, `<input[^>]*type="checkbox"[^>]*name="confirm"`, body,
		"the legacy <input type=\"checkbox\" name=\"confirm\"> delete confirmation must be gone (Phase 4.5 replaces with a modal)")

	// The modal itself must render with a
	// <dialog class="modal"> element. The ID
	// matches the data-modal-open target.
	assert.Contains(t, body, `<dialog id="delete-doc-1" class="modal">`,
		"the delete confirmation modal must render as a <dialog class=\"modal\"> with the matching id (Phase 4.5 contract)")

	// The modal contains the actual delete form
	// (POSTs to /documents/{id}/delete). Pin the
	// form action so the form submission target
	// is unambiguous.
	assert.Regexp(t, `<form[^>]*action="/documents/doc-1/delete"`, body,
		"the modal must contain the actual delete form (Phase 4.5 contract)")

	// The modal uses daisyUI's modal-box +
	// modal-action classes so it picks up the
	// standard modal styling. modal-action may
	// sit on a <form> (the action container) or
	// a <div> (the action row), so we accept
	// either.
	assert.Regexp(t, `<div[^>]*class="[^"]*\bmodal-box\b`, body,
		"the modal must use daisyUI's modal-box class for the content surface (Phase 4.5 contract)")
	assert.Regexp(t, `<(?:div|form)[^>]*class="[^"]*\bmodal-action\b`, body,
		"the modal must use daisyUI's modal-action class for the button row (Phase 4.5 contract)")
}

// TestDocuments_PaginationUsesJoin pins the Phase 4.6
// contract: the Previous / Next buttons render
// inside a <div class="join"> group so they read
// as a single visual unit. The pre-Phase-4.6 design
// rendered them as two independent <a class="btn">s
// — visually disconnected, easy to misread as two
// unrelated controls.
//
// The <div class="join"> is the daisyUI pattern
// from SKILL.md (Group buttons together with the
// join component).
func TestDocuments_PaginationUsesJoin(t *testing.T) {
	s := documentsOverhaulTestServer(t)

	// Build a data fixture with both Prev and
	// Next links present (i.e. multiple pages).
	data := documentsFallbackData{
		Title: "Ingested Documents",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
		},
		Total:        100,
		StartShowing: 26,
		EndShowing:   50,
		Limit:        25,
		PrevOffset:   0,
		NextOffset:   50,
		Documents:    []documentsFallbackRow{},
	}

	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, data))
	body := buf.String()

	// The Previous / Next links must be wrapped
	// in a <div class="join"> group. We accept
	// either "join mt-4" or any other class
	// combination that includes the join base
	// class.
	assert.Regexp(t, `<div[^>]*class="[^"]*\bjoin\b[^"]*"`, body,
		"the Previous / Next pagination must be wrapped in a <div class=\"join ...\"> group (Phase 4.6 contract)")

	// The Previous link carries join-item so it
	// inherits the group styling.
	assert.Regexp(t, `<a[^>]*class="[^"]*\bjoin-item\b[^"]*"[^>]*href="/documents\?[^"]*offset=0`, body,
		"the Previous link must use daisyUI's join-item class (Phase 4.6 contract)")

	// The Next link carries join-item too.
	assert.Regexp(t, `<a[^>]*class="[^"]*\bjoin-item\b[^"]*"[^>]*href="/documents\?[^"]*offset=50`, body,
		"the Next link must use daisyUI's join-item class (Phase 4.6 contract)")
}

// TestDocuments_DeleteHandlerRedirectsAndShowsToast
// pins the end-to-end Phase 4.5 / Phase 1.1
// integration: after a successful delete, the
// handler redirects to /documents (the page
// re-renders with one fewer row) AND chat.js
// shows a "Document deleted" toast via
// window.showToast (Phase 1.1 of plans/ux-overhaul.md).
//
// The toast is a client-side concern; this test
// pins the server-side behavior (redirect after
// delete) — the toast wiring is a separate test
// in chat.js.
func TestDocuments_DeleteHandlerRedirectsAndShowsToast(t *testing.T) {
	s := documentsOverhaulTestServer(t)

	form := "csrf_token=csrf-test&confirm=1"
	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/documents/doc-1/delete",
		strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "ragabast_csrf=csrf-test")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	// Phase 4.5: the handler redirects to
	// /documents on success. The exact status
	// is 302 (Found) per the existing
	// TestDocumentsDelete_HappyPath contract.
	assert.Equal(t, http.StatusFound, w.Code,
		"successful delete must redirect to /documents (Phase 4.5 contract)")
	loc, _ := w.Result().Location()
	require.NotNil(t, loc, "Location header must be set on the redirect")
	assert.Equal(t, "/documents", loc.String(),
		"successful delete must redirect to /documents")
}
