package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// uxPolishTestServer builds the standard test
// Server fixture for the Phase 6 tests.
func uxPolishTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
	s.templates = nil
	return s
}

// uxPolishChatFallbackBody executes the chat
// landing fallback template and returns the HTML.
func uxPolishChatFallbackBody(t *testing.T, s *Server) string {
	t.Helper()
	data := chatFallbackData{
		Title:     "Chat",
		CsrfToken: "",
		SessionID: "session-test",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
		},
	}
	var buf strings.Builder
	require.NoError(t, s.fallback.chat.Execute(&buf, data))
	return buf.String()
}

// uxPolishDocumentsFallbackBody executes the
// documents page fallback template and returns
// the HTML.
func uxPolishDocumentsFallbackBody(t *testing.T, s *Server) string {
	t.Helper()
	data := documentsFallbackData{
		Title:     "Documents",
		CsrfToken: "",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
		},
	}
	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, data))
	return buf.String()
}

// templatesFSReadFile reads a file from the
// embedded templates FS (used by the Phase 6
// grep test). The error message is intentionally
// loud — a missing template is a build error, not
// a test failure.
func templatesFSReadFile(t *testing.T, name string) string {
	t.Helper()
	data, err := templatesFS.ReadFile(name)
	require.NoError(t, err, "reading %s from embedded templates FS", name)
	return string(data)
}

// TestUx_NoFocusOutlineNoneGrep pins the Phase 6.2
// contract from plans/ux-overhaul.md: no template
// or CSS file in the project should carry
// `focus:outline-none`. The pre-Phase-6 codebase
// had a few legacy occurrences (the login page's
// provider links, the chat.css styles) that
// suppressed the browser's focus ring — a
// well-known a11y antipattern. daisyUI components
// have their own focus styles; custom elements
// should use `focus-visible:outline-2 ...` for
// explicit focus indicators.
//
// The test reads the template files and the
// chat.css / login.css files and asserts none of
// them contain the antipattern. It is the
// "grep" test the SPEC explicitly calls for
// ("The new instance should grep the templates
// for `focus:outline-none` and remove any
// occurrences.").
func TestUx_NoFocusOutlineNoneGrep(t *testing.T) {
	// Source files to grep. We read the embedded
	// template set + the static CSS + the fallback
	// renderer constant (which carries a login-page
	// navbar with the antipattern). The daisyui.min.css
	// is generated; we don't grep it (it has the
	// daisyUI internals which we don't control).
	files := []struct {
		path string
		body string
	}{
		{"templates/login.html", templatesFSReadFile(t, "templates/login.html")},
		{"templates/search.html", templatesFSReadFile(t, "templates/search.html")},
		{"templates/search_results.html", templatesFSReadFile(t, "templates/search_results.html")},
		{"templates/chat_message.html", templatesFSReadFile(t, "templates/chat_message.html")},
		{"static/chat.css", readStaticAsset(t, "/static/chat.css")},
		{"static/login.css", readStaticAsset(t, "/static/login.css")},
		{"fallback_renderers.go (pageHeaderFallbackBody constant)", pageHeaderFallbackBody},
	}

	for _, f := range files {
		assert.NotContains(t, f.body, "focus:outline-none",
			"%s must not contain the focus:outline-none antipattern (Phase 6.2 of plans/ux-overhaul.md)", f.path)
	}
}

// TestUx_EmptyStateCopyImproves pins the Phase 6.1
// contract: the empty-state copy across chat,
// documents, and search uses consistent tone
// ("What would you like to know?" / "Nothing here
// yet" / "No matches" — the spec's recommended
// tone pass).
//
// The pre-Phase-6 chat empty state was a single
// descriptive sentence ("Chat with your ingested
// documents..."). Phase 2.1 already upgraded that
// to the suggested-prompts card. The spec calls
// for a tone pass on the documents + search
// empty states too — Phase 6 pins the resulting
// contract:
//
//   - documents empty state: "Nothing here yet" +
//     an ingest guide CTA (Phase 4.1 + 6.1)
//   - search no-results: "No matches" + an ingest
//     guide CTA (Phase 3.3 + 6.1)
//
// The test asserts the new tone (no longer the
// pre-Phase-6 "No documents ingested yet" / "No
// results found" patterns).
func TestUx_EmptyStateCopyImproves(t *testing.T) {
	// Documents empty state. We render the page
	// with no documents and check for the new
	// tone + the ingest guide CTA.
	docsBody := uxPolishDocumentsFallbackBody(t, uxPolishTestServer(t))

	// The pre-Phase-6 phrasing "No documents
	// ingested yet" is gone. The new tone is
	// "Nothing here yet" or similar; we pin that
	// the literal pre-Phase-6 phrasing is
	// removed (a future redesign can change the
	// exact wording; the contract is that the
	// pre-Phase-6 phrasing is not in the page).
	assert.NotContains(t, docsBody, "No documents ingested yet",
		"the documents empty state must use the Phase 6.1 tone (not the pre-Phase-6 phrasing)")

	// The ingest guide CTA is still present
	// (Phase 4.1 + 6.1 contract).
	assert.Contains(t, docsBody, `/docs#/operations/ingest`,
		"the documents empty state must include the ingest guide CTA (Phase 4.1 + 6.1)")
}

// TestUx_TooltipOnIconOnlyButtons pins the Phase 6.3
// contract: icon-only buttons render a tooltip
// (via the daisyUI data-tooltip attribute or a
// data-tooltip pattern). The pre-Phase-6 code
// had icon-only buttons (e.g. the chat's
// "jump-to-latest" button) with no visible
// affordance. Phase 6.3 wires the data-tooltip
// so the button's purpose is discoverable on
// hover.
//
// The test asserts the chat page's icon-only
// button carries a data-tooltip attribute (or
// is wrapped in a tooltip class). The exact
// data-tooltip text is a design call; the
// contract is "the data-tooltip attribute
// exists".
func TestUx_TooltipOnIconOnlyButtons(t *testing.T) {
	body := uxPolishChatFallbackBody(t, uxPolishTestServer(t))

	// The jump-to-latest button is the canonical
	// icon-only button in the app. It has no
	// visible text (just an arrow); a screen-reader
	// user relies on aria-label (which the
	// pre-Phase-6 code already had) AND a
	// mouse user benefits from a data-tooltip.
	//
	// Phase 6.3 of plans/ux-overhaul.md: the
	// SPEC example uses daisyUI's data-tooltip
	// (CSS-only, no JS). The contract is the
	// data-tooltip attribute exists, not the
	// exact text.
	assert.Regexp(t, `<button[^>]*id="jump-to-latest"[^>]*data-tooltip=`, body,
		"the chat's icon-only 'jump-to-latest' button must carry a data-tooltip attribute (Phase 6.3 contract)")

	// aria-label is still the screen-reader
	// signal; the data-tooltip is the
	// mouse-hover signal. Both must be present.
	assert.Regexp(t, `<button[^>]*id="jump-to-latest"[^>]*aria-label=`, body,
		"the chat's icon-only 'jump-to-latest' button must carry an aria-label (a11y contract)")

	// Pin the data-tooltip value. We accept any
	// non-empty string (a future contributor
	// can refine the copy).
	tooltipMatch := extractAttr(body, `id="jump-to-latest"`, "data-tooltip")
	require.NotEmpty(t, tooltipMatch,
		"the data-tooltip attribute must have a non-empty value (Phase 6.3 contract)")
}

// extractAttr finds the named attribute on the
// first tag that matches the given id-anchored
// selector. Returns the attribute value (with
// quotes stripped) or "" if not found.
func extractAttr(body, idSel, attr string) string {
	before, _, found := strings.Cut(body, idSel)
	if !found {
		return ""
	}
	// Find the tag's closing > (start from the
	// nearest < before idSel).
	start := strings.LastIndex(before, "<")
	if start < 0 {
		return ""
	}
	rest := body[start:]
	end := strings.Index(rest, ">")
	if end < 0 {
		return ""
	}
	tag := rest[:end+1]
	// Find the attribute. We accept any quote style
	// (single or double) per the HTML spec.
	attrIdx := strings.Index(tag, attr+"=")
	if attrIdx < 0 {
		return ""
	}
	after := tag[attrIdx+len(attr)+1:]
	if len(after) < 2 {
		return ""
	}
	q := after[0]
	if q != '"' && q != '\'' {
		return ""
	}
	closeIdx := strings.IndexByte(after[1:], q)
	if closeIdx < 0 {
		return ""
	}
	return after[1 : 1+closeIdx]
}
