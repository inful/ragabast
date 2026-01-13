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

	ctx := buildQueryContext(results)
	require.Contains(t, ctx, "Result 1 (from Doc A):\nChunk A")
	require.Contains(t, ctx, "Result 2 (from Doc B):\nChunk B")
}

func TestBuildQueryPrompt_IncludesQueryAndContext(t *testing.T) {
	prompt := buildQueryPrompt("what is this?", "some context")
	require.Contains(t, prompt, "answer the question: what is this?")
	require.Contains(t, prompt, "Context:\nsome context")
}
