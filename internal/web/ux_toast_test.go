package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestUx_ToastContainerPresent pins the Phase 1.1 contract
// from plans/ux-overhaul.md: every full page renders a
// <div id="toast-container"> at the end of <body>. The
// container is empty in the static HTML — chat.js's
// showToast() helper injects alert children into it on
// demand.
//
// "Full page" is the surface that renders the navbar and
// has a meaningful <body>: chat, search, documents. The
// login page is excluded — the OAuth chooser doesn't carry
// chat.js and Phase 5 wires it in separately if it ever
// needs to surface a toast.
func TestUx_ToastContainerPresent(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	// Render each page so the contract is "the rendered
	// page declares the container" — fallback renderers
	// are executed; the embedded search template is read
	// as raw bytes (the toast container is a static
	// fragment in the file rather than a data-driven
	// substitution).
	chatBody := renderChatFallbackBody(t, s)
	searchBody := readTemplateFile(t, "templates/search.html")
	docsBody := renderDocumentsFallbackBody(t, s)

	for name, body := range map[string]string{
		"chat":      chatBody,
		"search":    searchBody,
		"documents": docsBody,
	} {
		t.Run(name, func(t *testing.T) {
			// The container must be a <div> with the
			// daisyUI toast class. The base class is
			// the daisyUI contract; the placement
			// modifiers (toast-top, toast-end) are
			// pinned separately so a future change
			// can move the container without losing
			// the "toasts are anchored to the
			// viewport, not the page content" intent.
			assert.Regexp(t,
				`<div[^>]*\bid="toast-container"[^>]*class="[^"]*\btoast\b[^"]*"`,
				body,
				"%s must render a <div id=\"toast-container\" class=\"toast …\">", name)

			// daisyUI's toast component positions
			// its children with `position: fixed`;
			// the daisyUI modifier class
			// `toast-top` pins the stack to the top
			// edge. We pin the modifier rather
			// than `position: fixed` because the
			// daisyUI implementation may change.
			assert.Regexp(t,
				`<div[^>]*\bid="toast-container"[^>]*\btoast-(?:top|bottom|start|end|center)\b`,
				body,
				"%s's toast container must declare a daisyUI toast placement modifier (toast-top / toast-end / etc.)", name)

			// The container should declare a
			// z-index so toasts layer above page
			// content. Tailwind's z-* utility
			// generates a CSS variable that
			// resolves at runtime; the daisyUI
			// pattern uses z-50.
			assert.Regexp(t,
				`<div[^>]*\bid="toast-container"[^>]*\bz-\d+\b`,
				body,
				"%s's toast container must declare a z-* utility so toasts layer above page content", name)

			// The container should sit at the end
			// of <body>. The exact "last child"
			// assertion is too tight (a future
			// contributor might add a <script>
			// tag after the container), so we
			// check that the container is near the
			// end of the body — within the last
			// 400 bytes.
			bodyEnd := body
			if len(bodyEnd) > 400 {
				bodyEnd = bodyEnd[len(bodyEnd)-400:]
			}
			assert.Contains(t, bodyEnd, `id="toast-container"`,
				"%s's toast container must be near the end of <body> (within the last 400 bytes of the rendered HTML)", name)
		})
	}
}

// TestUx_ShowToastHelperPresent pins the Phase 1.1 chat.js
// surface: a window.showToast(message, type, ttlMs) helper
// is exposed for the per-page call sites (Phase 4 documents
// delete success/error, Phase 5 login error, chat message
// send error).
//
// The contract: the helper exists in chat.js so a future
// contributor who removes it in a refactor gets a red test
// with a comment pointing at the spec.
func TestUx_ShowToastHelperPresent(t *testing.T) {
	body := readStaticAsset(t, "/static/chat.js")

	// showToast is the public API.
	require.Contains(t, body, "showToast",
		"chat.js must expose a showToast(message, type, ttlMs) helper (Phase 1.1 of plans/ux-overhaul.md)")

	// The four standard toast types — success / error /
	// warning / info — are the daisyUI alert-{type}
	// variants. Pin the literal tokens so a future
	// contributor who restricts the helper to one or two
	// types gets a red test (the design call is
	// "all four, even if some sites only use one").
	for _, kind := range []string{"success", "error", "warning", "info"} {
		assert.Contains(t, body, kind,
			"chat.js showToast must accept the %q type (daisyUI alert-%s variant)", kind, kind)
	}

	// The default TTL of 4000ms is the daisyUI docs
	// recommendation and matches user expectations from
	// every other product they've used. Pin the literal
	// number so a future contributor who drops the TTL
	// to 0 (which would render the toast for one frame
	// and dismiss it) gets a red test.
	assert.Regexp(t, `4000`, body,
		"chat.js showToast must use 4000ms as the default toast TTL (matches daisyUI docs and user expectations)")

	// The helper must operate on a #toast-container
	// element rather than a document-fragment or a
	// role=status live region. The daisyUI pattern is a
	// pre-rendered empty container that showToast
	// appends children to.
	assert.Regexp(t, `toast-container`, body,
		"chat.js showToast must operate on the #toast-container element (Phase 1.1 contract)")
}

// renderChatFallbackBody executes the chat landing fallback
// template and returns the rendered HTML.
func renderChatFallbackBody(t *testing.T, s *Server) string {
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

// renderDocumentsFallbackBody executes the documents page
// fallback template. Empty documents list — the page
// renders the empty state, which still needs the toast
// container for delete confirmations.
func renderDocumentsFallbackBody(t *testing.T, s *Server) string {
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

// readTemplateFile returns the raw bytes of an embedded
// template file. The /search toast-container contract is
// pinned against the file (rather than the executed
// template) because the container is a static fragment —
// the test is "the file declares the container", not "the
// rendered HTML for a given form input declares the
// container".
func readTemplateFile(t *testing.T, name string) string {
	t.Helper()
	data, err := templatesFS.ReadFile(name)
	require.NoError(t, err, "reading %s", name)
	return string(data)
}
