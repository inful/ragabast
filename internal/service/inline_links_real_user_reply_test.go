package service

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestInlineSourceLinks_RealUserReply pins the post-processor
// behavior against the EXACT reply and sources from the user's
// 2026-09-20 report ("the sources are still not linked
// correctly"). The LLM emitted [src:N] markers correctly; the
// regression was that the service layer never populated
// DocbuilderURL on the SearchResults, so InlineSourceLinks fell
// into the "no URL → bare text" branch.
//
// This test documents both halves of the contract:
//
//  1. When DocbuilderURL IS populated on the source, [src:1]
//     becomes a clickable markdown link to the docbuilder
//     permalink. This is the desired output.
//  2. When DocbuilderURL is NOT populated (the prior regression),
//     [src:1] falls through to bare title text. This documents
//     what the user was seeing.
//
// With the fix in QueryDebugWithOptions (it now calls
// enrichWithDocbuilderURLs on the results before returning them
// to the handler), case (2) cannot happen on the production
// path. The handler-level test (server_chat_inline_test.go)
// pins the end-to-end expectation.
func TestInlineSourceLinks_RealUserReply(t *testing.T) {
	// The exact reply the user received (from the chat-debug
	// log on 2026-09-20). Trimmed to a representative excerpt
	// that exercises [src:N] markers at multiple positions.
	reply := "DocBuilder is a **Go** CLI tool and daemon that " +
		"aggregates documentation from multiple **Git** repositories " +
		"into a unified **Hugo** static site, using the **Relearn** " +
		"Hugo theme (`github.com/McShelby/hugo-theme-relearn`) " +
		"[src:1][src:3][src:4].\n\n" +
		"Additional technologies and architectural elements:\n\n" +
		"- **Hugo** as the static site generator [src:1][src:2][src:3]\n" +
		"- **Relearn theme** as the hardcoded theme [src:4]\n" +
		"- **Prometheus**-compatible metrics [src:4]"

	// Sources as returned by the vector DB, with the fields
	// that vectorOps.Search actually populates. DocbuilderURL
	// is EMPTY here — that matches what the user's actual
	// production code path was returning (and is the cause of
	// the bug).
	sources := []models.SearchResult{
		{DocumentTitle: "High-Level System Architecture", DocumentID: "663991b1-bfe7-4c55-bd54-8f09e1120e06", UID: "high-level-system-architecture"},
		{DocumentTitle: "Architecture Overview", DocumentID: "c9a38b75-67d0-498f-ab60-e00dfd70e8ae", UID: "architecture-overview"},
		{DocumentTitle: "Getting Started with DocBuilder", DocumentID: "4a61e911-03a6-4769-9e15-63d304572860", UID: "getting-started"},
		{DocumentTitle: "Comprehensive Architecture Documentation", DocumentID: "86afd906-d6c4-4013-bc06-02f90e716825", UID: "comprehensive-architecture"},
		{DocumentTitle: "Comprehensive Architecture Documentation", DocumentID: "86afd906-d6c4-4013-bc06-02f90e716825", UID: "comprehensive-architecture"},
	}

	t.Run("without DocbuilderURL, markers become bare text (the bug)", func(t *testing.T) {
		got := InlineSourceLinks(reply, sources)

		// The [src:N] markers must be GONE — the post-processor
		// always substitutes them with text.
		require.NotContains(t, got, "[src:", "post-processor must consume every [src:N] marker")
		// But the substituted text is the bare title, because
		// there is no URL to wrap in. This is the user-visible
		// regression: three titles concatenated with no link.
		require.Contains(t, got, "Architecture Overview",
			"bare-title fallback: [src:2] becomes the source title with no link")
		require.Contains(t, got, "Comprehensive Architecture Documentation",
			"bare-title fallback: [src:3]/[src:4] become the source title with no link")
		require.NotContains(t, got, "](https://",
			"without DocbuilderURL the helper emits bare text — no markdown link syntax")
	})

	t.Run("with DocbuilderURL, markers become clickable links", func(t *testing.T) {
		// Simulate the post-fix world: enrichWithDocbuilderURLs
		// has been called on the sources.
		svc := &Service{config: withRagabastCfg("https://docs.example.com")}
		enriched := append([]models.SearchResult(nil), sources...)
		svc.enrichWithDocbuilderURLs(enriched)

		got := InlineSourceLinks(reply, enriched)

		require.NotContains(t, got, "[src:", "every marker must be substituted")
		require.Contains(t, got, "[Architecture Overview](https://docs.example.com/_uid/architecture-overview/)",
			"[src:2] becomes a markdown link to the source's docbuilder URL")
		require.Contains(t, got, "[Comprehensive Architecture Documentation](https://docs.example.com/_uid/comprehensive-architecture/)",
			"[src:3] and [src:4] become markdown links to the same source (linked twice in the reply)")
	})
}
