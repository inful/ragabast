package service

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestBuildQueryContext_FormatsResults(t *testing.T) {
	results := []models.SearchResult{
		{DocumentTitle: "Doc A", Content: "Chunk A", DocumentURLs: []string{"https://example.com/a"}},
		{DocumentTitle: "Doc B", Content: "Chunk B with https://example.com/b."},
	}

	items := buildQueryContextItems(results)
	require.Len(t, items, 2)
	require.Contains(t, items[0], "TITLE: Doc A")
	require.Contains(t, items[0], "CONTENT:")
	require.Contains(t, items[0], "Chunk A")
	require.Contains(t, items[0], "SOURCE_URLS:")
	require.Contains(t, items[0], "https://example.com/a")
	require.Contains(t, items[1], "TITLE: Doc B")
	require.Contains(t, items[1], "Chunk B")
	require.Contains(t, items[1], "SOURCE_URLS:")
	require.Contains(t, items[1], "https://example.com/b")
}

func TestBuildQueryPrompt_IncludesQueryAndContext(t *testing.T) {
	prompt, system, err := buildQueryPrompt("what is this?", []string{"TITLE: Doc A\nCONTENT:\nChunk A"}, nil)
	require.NoError(t, err)
	require.Contains(t, system, "You are a helpful assistant")
	require.Contains(t, system, "Links:")
	require.Contains(t, system, "you MUST include")
	require.Contains(t, system, "Only include URLs")
	require.Contains(t, system, "<context>")
	require.Contains(t, system, "<entry")
	require.Contains(t, system, "TITLE: Doc A")
	require.Contains(t, prompt, "Question: what is this?")
	require.Contains(t, prompt, "Answer:")
}

func TestBuildQueryPrompt_OmitsContextBlockWhenEmpty(t *testing.T) {
	prompt, system, err := buildQueryPrompt("what is this?", nil, nil)
	require.NoError(t, err)
	require.NotContains(t, system, "<context>")
	require.Contains(t, prompt, "Question: what is this?")
}

func TestBuildQueryPrompt_IncludesConversationHistory(t *testing.T) {
	history := []ChatMessage{
		{Role: "user", Content: "We are talking about ragabast."},
		{Role: "assistant", Content: "Ok."},
	}
	prompt, system, err := buildQueryPrompt("How do I deploy it?", []string{"TITLE: Doc A\nCONTENT:\nChunk A"}, history)
	require.NoError(t, err)
	require.Contains(t, system, "<conversation>")
	require.Contains(t, system, "role=\"user\"")
	require.Contains(t, system, "We are talking about ragabast")
	require.Contains(t, prompt, "Question: How do I deploy it?")
}
