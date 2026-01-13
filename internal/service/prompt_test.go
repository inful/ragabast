package service

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestBuildQueryContext_FormatsResults(t *testing.T) {
	results := []models.SearchResult{
		{DocumentTitle: "Doc A", Content: "Chunk A"},
		{DocumentTitle: "Doc B", Content: "Chunk B"},
	}

	items := buildQueryContextItems(results)
	require.Len(t, items, 2)
	require.Contains(t, items[0], "Doc A")
	require.Contains(t, items[0], "Chunk A")
	require.Contains(t, items[1], "Doc B")
	require.Contains(t, items[1], "Chunk B")
}

func TestBuildQueryPrompt_IncludesQueryAndContext(t *testing.T) {
	prompt, system, err := buildQueryPrompt("what is this?", []string{"Doc A: Chunk A"})
	require.NoError(t, err)
	require.Contains(t, system, "You are a helpful assistant")
	require.Contains(t, system, "<context>")
	require.Contains(t, system, "- Doc A: Chunk A")
	require.Contains(t, prompt, "Question: what is this?")
	require.Contains(t, prompt, "Answer:")
}

func TestBuildQueryPrompt_OmitsContextBlockWhenEmpty(t *testing.T) {
	prompt, system, err := buildQueryPrompt("what is this?", nil)
	require.NoError(t, err)
	require.NotContains(t, system, "<context>")
	require.Contains(t, prompt, "Question: what is this?")
}
