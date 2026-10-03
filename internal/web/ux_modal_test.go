package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUx_ModalHelpersPresent pins the Phase 1.2 contract
// from plans/ux-overhaul.md: chat.js exposes
// window.openModal(id) and window.closeModal(id) helpers
// that wrap the native <dialog>.showModal() and
// <dialog>.close() browser APIs.
//
// The pattern uses the native <dialog> element with the
// daisyUI `modal` class. Phase 4 (documents delete
// confirmation) is the first real consumer; this test
// pins the API surface so the helper exists when that
// phase lands. Without the helpers, a future contributor
// would have to either re-implement the open/close logic
// in every consumer or duplicate the showModal() /
// close() calls in every page's JS — both of which
// would erode the consistency the daisyUI modal pattern
// is meant to enforce.
func TestUx_ModalHelpersPresent(t *testing.T) {
	body := readStaticAsset(t, "/static/chat.js")

	// openModal must be a top-level global function
	// exposed on window. Pin the literal token so a
	// future contributor who renames it gets a red
	// test with a comment pointing at the spec.
	require.Contains(t, body, "openModal",
		"chat.js must expose an openModal(id) helper (Phase 1.2 of plans/ux-overhaul.md)")

	// closeModal is the matching pair. Phase 4
	// (documents delete confirmation) calls it on
	// Cancel, on backdrop click, and on successful
	// form submit.
	require.Contains(t, body, "closeModal",
		"chat.js must expose a closeModal(id) helper to match openModal (Phase 1.2 of plans/ux-overhaul.md)")

	// The helpers must be assigned to window.* so
	// per-page event handlers (Phase 4, Phase 5)
	// can call them without their own module
	// pattern. The exact assignment is
	// `window.openModal = openModal` (or similar);
	// pin that the function definition is paired
	// with the window assignment.
	assert.Regexp(t, `window\.openModal\s*=\s*openModal`, body,
		"openModal must be exposed on window so per-page event handlers can call it (Phase 1.2 contract)")
	assert.Regexp(t, `window\.closeModal\s*=\s*closeModal`, body,
		"closeModal must be exposed on window so per-page event handlers can call it (Phase 1.2 contract)")

	// Both helpers must act on the native <dialog>
	// showModal() / close() methods. The daisyUI
	// docs (SKILL.md) recommend the <dialog>
	// pattern over the popover API for accessibility
	// (the <dialog> element gets focus management
	// and ESC-to-close for free). Pin the literal
	// method names so a refactor that switches to
	// the popover API is a deliberate choice, not
	// an accident.
	assert.Regexp(t, `\.showModal\(`, body,
		"openModal must invoke the native <dialog>.showModal() method (the daisyUI modal pattern in SKILL.md)")
	assert.Regexp(t, `\.close\(`, body,
		"closeModal must invoke the native <dialog>.close() method (the daisyUI modal pattern in SKILL.md)")

	// The modal helpers must accept an id parameter
	// (string) and use it to look up the <dialog>
	// element. The convention is a #modal-{id} or
	// bare {id} selector — pin the getElementById
	// call so the helpers are tied to the
	// document.getElementById contract.
	assert.Regexp(t, `getElementById\(`, body,
		"the modal helpers must look up the dialog element via document.getElementById (per the daisyUI pattern in SKILL.md)")
}
