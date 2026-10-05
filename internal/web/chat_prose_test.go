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

// TestShippedCSS_OperatorBubbleMaxWidth pins the
// "operator (chat-start) bubbles feel like chat questions,
// not full-width prose paragraphs" contract. daisyUI's
// chat-bubble defaults to max-width: 90% of its grid
// column, which for the chat-log card at 56rem means an
// operator message stretches to ~510px — wide enough that
// a short question feels like a paragraph rather than a
// chat message. ChatGPT / Notion AI cap operator messages
// at ~70% of the scrollback width for the same reason:
// a narrower question reads as "you said something"
// rather than "here is a block of user text".
//
// chat.css scopes the override down to .chat-start
// .chat-bubble so the assistant side (chat-end) keeps
// daisyUI's default — long answers with code blocks need
// the full column width.
//
// Regression guard for the user-reported "chat still looks
// quite bad in the latest release" issue. The daisyUI
// 90% cap is technically correct for a chat table but
// reads as a paragraph here.
func TestShippedCSS_OperatorBubbleMaxWidth(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	assert.Regexp(t,
		`\.chat-start\s+\.chat-bubble\s*\{[^}]*max-width:`,
		css,
		"chat.css must override .chat-start .chat-bubble max-width so operator messages feel like chat questions rather than full-width prose paragraphs")
}

// TestShippedCSS_OperatorPreResetsProseMargin pins the
// "operator pre doesn't inherit the prose plugin's pre
// margins" contract. The chat-turn wrapper has class
// `prose chat-turn` so the assistant reply's typography
// gets prose's list / heading / code-block styling. But
// the same wrapper also contains the operator-side
// <pre class="chat-msg">, and the prose plugin's
//
//	.prose :where(pre) { margin-top: 1.71429em;
//	                     margin-bottom: 1.71429em; }
//
// applies to every descendant <pre> in the wrapper —
// including the operator pre. At prose-sm's 14px
// font-size that's ~24px of top margin AND ~24px of
// bottom margin, so a 2-line operator message ends up
// in a 130px-tall bubble with ~50% empty vertical
// space. The user reported the bubbles as "squashed" —
// the actual issue is that the pre's prose-default
// margins push the visible text into a small fraction
// of the bubble's vertical area.
//
// The fix in chat.css targets pre.chat-msg (without
// the .prose class) so the operator pre gets the
// margin reset. Assistant pre blocks (which carry
// chat-msg.prose.prose-sm) keep the prose margins —
// code fences still get the surrounding whitespace
// the prose plugin's design calls for.
func TestShippedCSS_OperatorPreResetsProseMargin(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	// The selector must be pre.chat-msg (or a parent that
	// includes the operator pre) AND the property must
	// reset margin-block to 0 so the prose pre's
	// 1.71429em top/bottom margins don't push the operator
	// text into a small fraction of the bubble.
	assert.Regexp(t,
		`pre\.chat-msg\s*\{[^}]*margin-block:\s*0`,
		css,
		"chat.css must reset pre.chat-msg margin-block to 0 so the operator pre doesn't inherit the prose plugin's ~24px top/bottom margins (which were making the bubbles look squashed by pushing the text into a small fraction of the vertical area)")
}

// TestShippedCSS_ChatMsgLinksUseThemeAwareColor pins the
// dark-mode link readability contract. The
// @tailwindcss/typography plugin sets `.prose :where(a)` to
// `color: var(--tw-prose-links)`, a hardcoded dark gray-blue
// (oklch(21% .034 264.665)). That color is fine on a light
// background, but inside the chat-bubble in dark mode it
// lands as dark text on a dark bubble — invisible.
//
// The fix in chat.css overrides the prose plugin's link
// color with daisyUI's theme-aware primary token. The
// OS-driven prefers-color-scheme flip re-themes
// --color-primary alongside the rest of the daisyUI palette.
// Scoping to `.chat-msg` (not `.prose`) keeps the override
// surgical — search-result chunks and the landing placeholder
// keep the plugin's intended link color.
//
// Regression guard for the user-reported "links unreadable
// in dark mode" issue (the prose plugin's hardcoded dark
// link color was the root cause).
func TestShippedCSS_ChatMsgLinksUseThemeAwareColor(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	// The override must scope to .chat-msg a (not .prose a
	// globally) so only chat messages pick up the theme-
	// aware color. Search-result chunks (also .prose) keep
	// the plugin's default link color.
	//
	// The color must reference a theme-aware variable, not
	// a hardcoded oklch/rgb/hex value. var(--color-primary)
	// is daisyUI's standard "link/CTA" color and is the
	// canonical choice for chat message links (Slack,
	// Linear, Notion AI all use the brand primary).
	assert.Regexp(t,
		`\.chat-msg\s+a\s*\{[^}]*color\s*:\s*var\(--color-primary\)`,
		css,
		"chat.css must override link color inside .chat-msg with var(--color-primary) so dark-mode links adapt to the daisyUI theme (the prose plugin's --tw-prose-links is hardcoded dark and invisible in dark mode)")
}

// TestShippedCSS_OperatorPreResetsProseBackground pins the
// "operator bubble is not two-toned" contract. The chat-turn
// wrapper carries `prose` so the assistant reply inherits
// the typography plugin's list / heading / code-block styling.
// The operator's `<pre class="chat-msg">` sits inside that
// same wrapper, which means the plugin's `:where(pre)` rule
// applies `--tw-prose-pre-bg` (a hardcoded dark navy
// oklch(27.8% .033 256.848)) to the operator pre — leaving
// the operator bubble looking two-toned (the bubble's
// chat-bubble-primary on the outside, the prose-pre-bg on the
// inside).
//
// The fix in chat.css scopes `background-color: transparent`
// to `.chat-msg:not(.prose)` so only the operator pre
// (which has `chat-msg` but NOT `prose`) gets the reset.
// The assistant reply div carries `chat-msg prose prose-sm`
// and intentionally keeps the plugin's pre styling for
// fenced code blocks rendered inside.
func TestShippedCSS_OperatorPreResetsProseBackground(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	// The selector must be `.chat-msg:not(.prose)` (or
	// equivalent specificity) so the assistant reply's
	// prose pre blocks keep the plugin's background.
	// Accept any whitespace around `:not(.prose)` — the
	// preprocessor doesn't normalize it.
	assert.Regexp(t,
		`\.chat-msg:not\(\.prose\)\s*\{[^}]*background-color\s*:\s*transparent`,
		css,
		"chat.css must reset background-color on .chat-msg:not(.prose) so the operator pre does not inherit the prose plugin's --tw-prose-pre-bg (operator bubble should be one solid chat-bubble-primary color)")
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
