package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// renderAndCollapse runs renderChatMarkdownToSafeHTML and trims
// outer whitespace so substring assertions stay readable. The
// markdown-to-HTML converter is free to add or strip whitespace
// in ways that don't change the security-relevant output.
func renderAndCollapse(t *testing.T, md string) string {
	t.Helper()
	out, err := renderChatMarkdownToSafeHTML(md)
	require.NoError(t, err)
	return strings.Join(strings.Fields(out), " ")
}

func TestRenderChatMarkdown_BasicParagraphPreserved(t *testing.T) {
	out := renderAndCollapse(t, "hello world")

	require.Contains(t, out, "hello world")
}

func TestRenderChatMarkdown_LinkHasTargetBlankAndNoopener(t *testing.T) {
	out := renderAndCollapse(t, "see [docs](https://example.com/docs)")

	require.Contains(t, out, `href="https://example.com/docs"`,
		"the link should be preserved")
	require.Contains(t, out, `target="_blank"`,
		"links should open in a new tab")
	require.Contains(t, out, `rel="noopener noreferrer"`,
		"links should advertise noopener/noreferrer")
}

// TestRenderChatMarkdown_StripsScriptTag is the headline security
// test: a hostile LLM that emits a <script> tag must not survive
// into the page. We don't assert that the inner text disappears —
// bluemonday may keep it as visible text content, which is the
// safe behavior. What matters is that no executable script tag
// reaches the browser.
func TestRenderChatMarkdown_StripsScriptTag(t *testing.T) {
	out := renderAndCollapse(t, "before <script>alert(1)</script> after")

	require.NotContains(t, strings.ToLower(out), "<script",
		"raw <script> tags must be stripped")
	require.Contains(t, out, "before",
		"text before the script should be preserved")
	require.Contains(t, out, "after",
		"text after the script should be preserved")
}

func TestRenderChatMarkdown_StripsIframeTag(t *testing.T) {
	out := renderAndCollapse(t, "before <iframe src=\"https://evil.example/\"></iframe> after")

	require.NotContains(t, strings.ToLower(out), "<iframe",
		"iframes must be stripped — chat content lives inside the same origin")
	require.Contains(t, out, "before")
	require.Contains(t, out, "after")
}

func TestRenderChatMarkdown_StripsInlineEventHandlers(t *testing.T) {
	cases := []string{
		`<img src="x" onerror="alert(1)">`,
		`<a href="https://example.com" onclick="steal()">click</a>`,
		`<div onmouseover="alert(1)">hover</div>`,
		`<svg onload="alert(1)"></svg>`,
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			out := renderAndCollapse(t, input)
			lower := strings.ToLower(out)
			require.NotContains(t, lower, "onerror=")
			require.NotContains(t, lower, "onclick=")
			require.NotContains(t, lower, "onmouseover=")
			require.NotContains(t, lower, "onload=")
		})
	}
}

// TestRenderChatMarkdown_StripsJavascriptURL guards the classic
// javascript: pseudo-protocol attack, both in raw HTML and via
// markdown link syntax.
func TestRenderChatMarkdown_StripsJavascriptURL(t *testing.T) {
	t.Run("raw anchor", func(t *testing.T) {
		out := renderAndCollapse(t, `<a href="javascript:alert(1)">click</a>`)
		require.NotContains(t, strings.ToLower(out), "javascript:",
			"javascript: URLs must be removed")
	})

	t.Run("markdown link", func(t *testing.T) {
		out := renderAndCollapse(t, "[click](javascript:alert(1))")
		require.NotContains(t, strings.ToLower(out), "javascript:")
	})
}

// TestRenderChatMarkdown_AllowsSafeMarkdown is the positive
// counterpart to the security tests above: common markdown that
// we *do* want should render.
func TestRenderChatMarkdown_AllowsSafeMarkdown(t *testing.T) {
	t.Run("bold and code", func(t *testing.T) {
		out := renderAndCollapse(t, "use **`go test`** to run **bold** and `code`.")
		require.Contains(t, out, "<strong><code>go test</code></strong>")
		require.Contains(t, out, "<strong>bold</strong>")
		require.Contains(t, out, "<code>code</code>")
	})

	t.Run("list", func(t *testing.T) {
		out := renderAndCollapse(t, "- one\n- two\n")
		require.Contains(t, out, "<ul>")
		require.Contains(t, out, "<li>one</li>")
		require.Contains(t, out, "<li>two</li>")
	})

	t.Run("autolink", func(t *testing.T) {
		out := renderAndCollapse(t, "see https://example.com/path for more")
		require.Contains(t, out, `href="https://example.com/path"`)
	})
}
