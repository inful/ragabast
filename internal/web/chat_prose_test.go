package web

// Phase 7 of plans/ux-overhaul.md (prose typography follow-on).
//
// Before this phase the assistant chat reply wrapped the
// markdown-rendered HTML in `<div class="chat-msg prose">`.
// The `prose` class is the contract for "render markdown-shaped
// content with proper typography". The original Bulma 1.x era
// shipped its own `.content` reset; the daisyUI migration
// swapped that for `prose`, but the supporting plugin
// (`@tailwindcss/typography`) was never installed. Result:
// lists rendered with raw browser defaults (40px left margin,
// tiny bullet character, no spacing between items, no
// line-height harmony with surrounding text).
//
// This file pins the contract:
//   1. The chat message fragment applies `prose` + `prose-sm`
//      to the assistant reply (chat-appropriate sizing).
//   2. The shipped CSS carries the typography plugin's list /
//      paragraph / heading / code-block selectors (the visual
//      contract).
//   3. chat.css overrides `color` inside `.chat-msg.prose` so
//      the chat-bubble's own color wins over the plugin's
//      gray body text (otherwise the prose plugin would tint
//      every reply a hardcoded gray).
//   4. The chat handler actually renders list / paragraph /
//      heading / code block markdown as the corresponding
//      HTML elements (sanity pin beyond the search-side
//      RendersLists contract).

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestChatMessageFragment_AssistantReplyUsesProseSm pins the
// "typography applied to the assistant reply, at the chat-
// appropriate size" contract introduced in Phase 7 of
// plans/ux-overhaul.md. Without prose-sm (or equivalent
// size modifier) the default prose-base 1rem body text
// looks oversized inside the chat-bubble, which is the
// visual regression this phase exists to prevent.
func TestChatMessageFragment_AssistantReplyUsesProseSm(t *testing.T) {
	body := fetchChatMessageBody(t, "")

	// The assistant reply div carries the chat-msg class
	// (so chat.css's overflow-wrap / word-break rules
	// apply) AND the prose + prose-sm classes (so the
	// @tailwindcss/typography plugin applies its chat-
	// sized typography).
	assert.Regexp(t,
		`<div[^>]*class="[^"]*\bchat-msg\b[^"]*\bprose\b[^"]*\bprose-sm\b[^"]*"`,
		body,
		"the assistant chat-bubble content must carry chat-msg + prose + prose-sm (Phase 7 prose typography)")
}

// TestChatMessageFragment_OperatorMessagePlainText pins
// the "operator message is rendered as plain text inside a
// <pre>, not as markdown" contract. The operator types
// raw text into the form; that text is not markdown-rendered
// (no <p> wrappers, no list parsing). The operator bubble
// uses pre.chat-msg so the visual treatment stays
// consistent with the prose path on the assistant side.
func TestChatMessageFragment_OperatorMessagePlainText(t *testing.T) {
	body := fetchChatMessageBody(t, "")

	// The operator bubble wraps the raw user input in a
	// <pre class="chat-msg"> — no `prose` class (it's not
	// markdown).
	preIdx := strings.Index(body, "<pre class=\"chat-msg\">")
	require.GreaterOrEqual(t, preIdx, 0,
		"the operator message must render in <pre class=\"chat-msg\"> (no prose, no markdown)")
}

// TestChatMessageFragment_HandlesListAndParagraphMarkdown pins
// that the handler renders the markdown the LLM emits. The
// search-side test (TestHandleSearchSubmit_RendersLists)
// covers the renderer; this test pins the chat side end-to-
// end so a future refactor that splits the chat rendering
// from the search rendering doesn't silently regress.
func TestChatMessageFragment_HandlesListAndParagraphMarkdown(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		queryAnswer: "Here is a list:\n\n- one\n- two\n- three\n\nAnd a paragraph.",
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("message", "show me a list")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	body := w.Body.String()

	require.Contains(t, body, "<ul",
		"the chat assistant reply must render markdown lists as <ul> (Phase 7 prose typography)")
	require.Contains(t, body, "<li>one</li>",
		"the chat assistant reply must wrap each list item in <li>")
	require.NotContains(t, body, "\n- one\n",
		"raw bullet-marker lines must NOT appear in the rendered HTML")
	require.Contains(t, body, "<p>",
		"the chat assistant reply must wrap paragraphs in <p>")
	require.Contains(t, body, "And a paragraph.",
		"the chat assistant reply must preserve paragraph text content")
}

// TestChatMessageFragment_HandlesHeadingAndCodeBlock pins
// the heading + code-block rendering in the chat side. Same
// reason as the list test: search-side covers the renderer,
// this pins the chat handler uses it.
func TestChatMessageFragment_HandlesHeadingAndCodeBlock(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		queryAnswer: "## Heading\n\n```go\nfmt.Println(\"hi\")\n```\n",
	}
	s := NewServer(cfg, svc)

	form := url.Values{}
	form.Set("message", "show me a heading")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	body := w.Body.String()

	require.Contains(t, body, "<h2",
		"the chat assistant reply must render markdown headings as <h2> (Phase 7 prose typography)")
	require.Contains(t, body, "Heading",
		"the heading text must be preserved")
	require.Contains(t, body, "<pre>",
		"the chat assistant reply must render fenced code blocks as <pre>")
	require.Contains(t, body, "<code",
		"the chat assistant reply must wrap code blocks in <code>")
	require.NotContains(t, body, "```",
		"raw triple-backtick fences must NOT appear in the rendered HTML")
}

// TestShippedCSS_TypographyPluginLoaded pins the visual
// contract: the shipped daisyui.min.css must include the
// @tailwindcss/typography plugin's list / paragraph /
// heading / code-block selectors. Without this, the prose
// class is a hollow contract — markup says "prose" but the
// rules don't exist in the bundle, so the browser falls
// back to UA defaults.
//
// We assert on the .prose class names that the plugin
// emits. The exact form (`.prose :where(ul):not(...)` vs
// plain `.prose ul`) varies across plugin versions, so we
// assert on the more stable inner selectors that any
// working typography plugin must produce.
func TestShippedCSS_TypographyPluginLoaded(t *testing.T) {
	css := readStaticAsset(t, "/static/daisyui.min.css")
	require.NotEmpty(t, css, "daisyui.min.css must be readable from the embedded bundle")

	// Phase 7 of plans/ux-overhaul.md: the typography
	// plugin must be wired into static/src/daisyui.css so
	// the .prose class actually styles lists etc.
	// We assert that each prose-namespaced selector the
	// plugin emits is present. The selectors are stable
	// across @tailwindcss/typography v0.5+ (the version
	// range compatible with Tailwind v4).
	mustContain := []string{
		// Lists — the headline Phase 7 win.
		".prose :where(ol):not(:where([class~=not-prose]",
		".prose :where(ul):not(:where([class~=not-prose]",
		".prose :where(li):not(:where([class~=not-prose]",

		// Paragraphs and headings — same scope, same
		// typography plugin.
		".prose :where(p):not(:where([class~=not-prose]",
		".prose :where(h2):not(:where([class~=not-prose]",
		".prose :where(h3):not(:where([class~=not-prose]",

		// Code blocks — required for any LLM that
		// emits a fenced ```lang block.
		".prose :where(code):not(:where([class~=not-prose]",
		".prose :where(pre):not(:where([class~=not-prose]",
	}
	for _, fragment := range mustContain {
		assert.Contains(t, css, fragment,
			"daisyui.min.css must include %q — the @tailwindcss/typography plugin is not loaded (Phase 7 prose typography)", fragment)
	}
}

// TestShippedCSS_ChatMsgProseInheritsColor pins the
// "chat-bubble color wins over prose color" contract. The
// typography plugin defaults `.prose` body text to a gray
// (`--tw-prose-body`). Inside a chat-bubble that gray
// would override the bubble's own themed color, leaving
// every reply visually gray instead of theme-aware. The
// fix is a single scoped rule in chat.css:
//
//	.chat-msg.prose { color: inherit }
//
// This test reads chat.css and asserts the override is
// present.
func TestShippedCSS_ChatMsgProseInheritsColor(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	// The override must scope to .chat-msg.prose (or
	// equivalent specificity) so it only applies inside
	// chat-bubble, not to every .prose element in the app
	// (search results keep the plugin's default color).
	assert.Regexp(t,
		`\.chat-msg\.prose\s*\{[^}]*color\s*:\s*inherit`,
		css,
		"chat.css must override color inside .chat-msg.prose so the chat-bubble color wins over the typography plugin's gray body text (Phase 7)")
}

// TestShippedCSS_BundleSizeBudgetReasonable pins that the
// daisyui.min.css bundle stays under the existing 250 KB
// budget after the typography plugin is added. The
// existing budget check is in static_test.go; this is a
// tighter Phase-7-specific pin so a runaway plugin
// upgrade is caught immediately.
func TestShippedCSS_BundleSizeBudgetReasonable(t *testing.T) {
	css := readStaticAsset(t, "/static/daisyui.min.css")
	require.NotEmpty(t, css, "daisyui.min.css must be readable from the embedded bundle")

	// The existing budget is 250 KB (static_test.go).
	// Phase 7's tighter pin: 200 KB. The typography
	// plugin should add well under 100 KB to a 85 KB
	// bundle, but a runaway upgrade (e.g., a future
	// plugin version that double-emits rules) should
	// fail this test rather than slip past the wider
	// check.
	const phase7BudgetBytes = 200 * 1024
	assert.Less(t, len(css), phase7BudgetBytes,
		"daisyui.min.css must stay under %d bytes after Phase 7 (got %d bytes) - a runaway plugin upgrade is bloating the bundle",
		phase7BudgetBytes, len(css))
}
