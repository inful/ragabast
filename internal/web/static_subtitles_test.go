package web

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChatFallback_SubtitleTightened pins the chat landing page
// carries a one-line subtitle that explains what the page does,
// not a rephrase of the h1. Issue #93.
func TestChatFallback_SubtitleTightened(t *testing.T) {
	require.NotContains(t, chatFallbackBody, "Ask questions against the ingested documents.",
		"the chat subtitle must be tighter than the original rephrase of the h1")

	// The new subtitle must mention something the user needs to
	// know that the h1 alone doesn't convey — e.g. that the
	// chat is RAG-backed by the ingested corpus.
	require.Regexp(t, `(?i)subtitle.*?[Rr]etriev`, chatFallbackBody,
		"the chat subtitle should mention retrieval / RAG so the user knows answers come from the corpus")
}

// TestSearch_SubtitleTightened pins the same for the /search
// page: the subtitle must convey what the page does without
// restating the h1.
func TestSearch_SubtitleTightened(t *testing.T) {
	body := searchHTML(t)

	require.NotContains(t, body, "Semantic search across the ingested corpus.",
		"the search subtitle must be tighter than the original rephrase of the h1")

	// The new subtitle should clarify the hybrid / keyword
	// behavior so the user understands what they're getting.
	require.Regexp(t, `(?i)subtitle.*?(hybrid|keyword|filter)`, body,
		"the search subtitle should mention hybrid / keyword / filters so the user knows what the form does")
}

// searchHTML returns the embedded /search template body for
// per-asset assertions. Templates live under templates/ in the
// templatesFS embed, separate from the static/ assets.
func searchHTML(t *testing.T) string {
	t.Helper()
	data, err := templatesFS.ReadFile("templates/search.html")
	require.NoError(t, err, "reading templates/search.html")
	return string(data)
}
