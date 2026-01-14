package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppendLinksSection_AppendsWhenMissing(t *testing.T) {
	out := appendLinksSection("Answer text.", []string{"https://example.com/a", "https://example.com/b"})
	require.Contains(t, out, "Answer text.")
	require.Contains(t, out, "\n\nLinks:\n")
	require.Contains(t, out, "- https://example.com/a")
	require.Contains(t, out, "- https://example.com/b")
}

func TestAppendLinksSection_RewritesExistingLinksSectionToCanonical(t *testing.T) {
	in := "Answer text.\n\nLinks:\n- https://example.com/a"
	out := appendLinksSection(in, []string{"https://example.com/a", "https://example.com/b"})
	require.Contains(t, out, "Answer text.")
	require.Contains(t, out, "\n\nLinks:\n")
	require.Contains(t, out, "- https://example.com/a")
	require.Contains(t, out, "- https://example.com/b")
}

func TestAppendLinksSection_RewritesInlineLinksToCanonical(t *testing.T) {
	in := "Answer text.\n\nLinks: https://example.com/a, https://example.com/b"
	out := appendLinksSection(in, []string{"https://example.com/a", "https://example.com/b"})
	require.Contains(t, out, "Answer text.")
	require.Contains(t, out, "\n\nLinks:\n")
	require.Contains(t, out, "- https://example.com/a")
	require.Contains(t, out, "- https://example.com/b")
	require.NotContains(t, out, "Links: https://")
}

func TestAppendLinksSection_StripsHallucinatedLinksWhenNoSources(t *testing.T) {
	in := "Install: https://ragabast.dev/docs/installation\n\nLinks:\n- https://ragabast.dev/docs/installation"
	out := appendLinksSection(in, nil)
	require.NotContains(t, out, "ragabast.dev")
	require.NotContains(t, out, "Links:")
}

func TestAppendLinksSection_RewritesToAllowedSourcesOnly(t *testing.T) {
	allowed := []string{"https://example.com/docs/sample", "https://github.com/ragabast"}
	in := "Do stuff.\n\nLinks:\n- https://ragabast.dev/docs/installation\n- https://example.com/docs/sample"
	out := appendLinksSection(in, allowed)
	require.Contains(t, out, "Links:")
	require.Contains(t, out, "- https://example.com/docs/sample")
	require.Contains(t, out, "- https://github.com/ragabast")
	require.NotContains(t, out, "ragabast.dev")
}

func TestAppendLinksSection_PreservesTrailingPunctuationForAllowedURL(t *testing.T) {
	allowed := []string{"https://example.com/a"}
	in := "See (https://example.com/a)."
	out := appendLinksSection(in, allowed)
	require.Contains(t, out, "See (https://example.com/a).")
	require.Contains(t, out, "\n\nLinks:\n")
	require.Contains(t, out, "- https://example.com/a")
}

func TestAppendLinksSection_StripsDisallowedURLButKeepsPunctuation(t *testing.T) {
	allowed := []string{"https://example.com/a"}
	in := "See (https://evil.example/a)."
	out := appendLinksSection(in, allowed)
	require.NotContains(t, out, "https://evil.example")
	require.Contains(t, out, "See ().")
}

func TestAppendLinksSection_PreservesAllowedMarkdownLinkURL(t *testing.T) {
	allowed := []string{"https://example.com/a"}
	in := "See [doc](https://example.com/a)."
	out := appendLinksSection(in, allowed)
	require.Contains(t, out, "See [doc](https://example.com/a).")
	require.Contains(t, out, "\n\nLinks:\n")
	require.Contains(t, out, "- https://example.com/a")
}

func TestAppendLinksSection_StripsDisallowedMarkdownLinkURL(t *testing.T) {
	allowed := []string{"https://example.com/a"}
	in := "See [doc](https://evil.example/a)."
	out := appendLinksSection(in, allowed)
	require.NotContains(t, out, "https://evil.example")
	require.Contains(t, out, "See doc.")
}

func TestAppendLinksSection_StripsDisallowedBacktickedURLWithoutBreakingBackticks(t *testing.T) {
	allowed := []string{"https://example.com/a"}
	in := "Use `curl https://evil.example/a` to fetch."
	out := appendLinksSection(in, allowed)
	require.NotContains(t, out, "https://evil.example")
	require.Contains(t, out, "Use `curl` to fetch.")
}

func TestAppendLinksSection_StripsDisallowedMarkdownLink_WhenLinkTextIsURL(t *testing.T) {
	allowed := []string{"https://example.com/a"}
	in := "Follow the instructions: [https://ollama.com](https://ollama.com)."
	out := appendLinksSection(in, allowed)
	require.NotContains(t, out, "ollama.com")
	require.NotContains(t, out, "[")
}
