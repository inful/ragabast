package service

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestInlineSourceLinks_ReplacesMarkerWithDocbuilderURL pins
// the headline behavior: an [src:N] marker in the LLM reply
// becomes an inline markdown link to source N's docbuilder URL.
// The link text is the document title (falling back to the
// document_id if no title). The markdown→HTML renderer that
// already runs in the chat handler converts the resulting
// markdown link to a clickable <a> tag.
func TestInlineSourceLinks_ReplacesMarkerWithDocbuilderURL(t *testing.T) {
	sources := []models.SearchResult{
		{
			DocumentTitle: "ADR 001",
			DocumentID:    "doc-1",
			UID:           "adr-001",
			DocbuilderURL: "https://docs.example.com/_uid/adr-001/",
		},
	}
	got := InlineSourceLinks("See [src:0] for details.", sources)
	require.Equal(t,
		"See [ADR 001](https://docs.example.com/_uid/adr-001/) for details.",
		got,
		"[src:0] must become a markdown link with the docbuilder URL")
}

// TestInlineSourceLinks_FallsBackToFirstDocumentURL pins the
// fallback for sources that have a user-set DocumentURL but no
// DocbuilderURL (ragabast.docbuilder_base_url is not configured).
// The first user-set URL becomes the link target.
func TestInlineSourceLinks_FallsBackToFirstDocumentURL(t *testing.T) {
	sources := []models.SearchResult{
		{
			DocumentTitle: "Manual",
			DocumentID:    "doc-1",
			DocumentURLs:  []string{"https://example.com/manual", "https://example.com/mirror"},
			// no DocbuilderURL
		},
	}
	got := InlineSourceLinks("See [src:0].", sources)
	require.Equal(t,
		"See [Manual](https://example.com/manual).",
		got,
		"first DocumentURL must be the link target when no DocbuilderURL is set")
}

// TestInlineSourceLinks_NoURLEmitsBareTitle pins the case where
// a source has no URL anywhere: the marker becomes just the
// title (no brackets, no link) so the user sees a meaningful
// reference but no broken "[title]()" markdown.
func TestInlineSourceLinks_NoURLEmitsBareTitle(t *testing.T) {
	sources := []models.SearchResult{
		{
			DocumentTitle: "Note",
			DocumentID:    "doc-1",
			// no URLs of any kind
		},
	}
	got := InlineSourceLinks("See [src:0].", sources)
	require.Equal(t, "See Note.", got,
		"no URL must produce bare title text, not an empty markdown link")
}

// TestInlineSourceLinks_NoTitleFallsBackToDocumentID pins the
// case where the source has no DocumentTitle: we use the
// DocumentID as the link text so the user still sees
// *something* identifying.
func TestInlineSourceLinks_NoTitleFallsBackToDocumentID(t *testing.T) {
	sources := []models.SearchResult{
		{
			DocumentID:    "doc-42",
			DocbuilderURL: "https://docs.example.com/_uid/doc-42/",
		},
	}
	got := InlineSourceLinks("See [src:0].", sources)
	require.Equal(t,
		"See [doc-42](https://docs.example.com/_uid/doc-42/).",
		got,
		"no title must fall back to DocumentID for the link text")
}

// TestInlineSourceLinks_OutOfBoundsLeavesMarker pins the case
// where the LLM emits a marker for an index that doesn't exist
// in the sources (e.g. it cited [src:7] but only 3 sources were
// retrieved). The marker must be left as-is so the user (and
// any future debugging) can see what the LLM tried to do.
func TestInlineSourceLinks_OutOfBoundsLeavesMarker(t *testing.T) {
	sources := []models.SearchResult{
		{DocumentTitle: "ADR 001", DocbuilderURL: "https://docs.example.com/_uid/adr-001/"},
	}
	got := InlineSourceLinks("See [src:7].", sources)
	require.Equal(t, "See [src:7].", got,
		"out-of-bounds [src:N] must pass through unchanged")
}

// TestInlineSourceLinks_HandlesMultipleMarkers pins that
// multiple markers in the same response are all replaced.
func TestInlineSourceLinks_HandlesMultipleMarkers(t *testing.T) {
	sources := []models.SearchResult{
		{DocumentTitle: "ADR 001", DocbuilderURL: "https://docs.example.com/_uid/adr-001/"},
		{DocumentTitle: "ADR 002", DocbuilderURL: "https://docs.example.com/_uid/adr-002/"},
	}
	got := InlineSourceLinks(
		"According to [src:0], and later confirmed by [src:1].",
		sources,
	)
	require.Equal(t,
		"According to [ADR 001](https://docs.example.com/_uid/adr-001/), and later confirmed by [ADR 002](https://docs.example.com/_uid/adr-002/).",
		got)
}

// TestInlineSourceLinks_NoSourcesLeavesAllMarkersAsIs pins the
// degenerate case: zero sources. Any [src:N] marker stays as-is
// (the LLM wouldn't have emitted any with no context, but the
// post-processor must not crash).
func TestInlineSourceLinks_NoSourcesLeavesAllMarkersAsIs(t *testing.T) {
	got := InlineSourceLinks("Hello [src:0] world.", nil)
	require.Equal(t, "Hello [src:0] world.", got)
}

// TestInlineSourceLinks_DoesNotMatchPlainBrackets pins that the
// regex is specific to [src:N] and does not eat other bracket
// patterns. Plain "[1]" (the current prompt's citation syntax)
// or "[2025]" must pass through unchanged — otherwise we'd
// regress to eating year references and similar.
func TestInlineSourceLinks_DoesNotMatchPlainBrackets(t *testing.T) {
	sources := []models.SearchResult{
		{DocumentTitle: "ADR 001", DocbuilderURL: "https://docs.example.com/_uid/adr-001/"},
	}
	got := InlineSourceLinks("Year [2025] saw [1] events.", sources)
	require.Equal(t, "Year [2025] saw [1] events.", got,
		"the [src:N] regex must not eat [N] or [year] patterns")
}
