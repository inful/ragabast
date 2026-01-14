package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFrontmatterSuggestionJSON_AcceptsStrictJSON(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON(`{"description":"d","categories":["Guides"],"tags":["rag"],"custom_tags":["x"]}`)
	require.NoError(t, err)
	require.Equal(t, "d", s.Description)
	require.Equal(t, []string{"Guides"}, s.Categories)
	require.Equal(t, []string{"rag"}, s.Tags)
	require.Equal(t, []string{"x"}, s.CustomTags)
}

func TestParseFrontmatterSuggestionJSON_RepairsBareKeysAndTrailingCommas(t *testing.T) {
	// This matches the kind of output that triggers: invalid character '}' looking for beginning of object key string
	// (e.g. trailing comma before }).
	s, err := parseFrontmatterSuggestionJSON(`{
  description: "Getting started with Ragabast",
  categories: ["Guides",],
  tags: ["rag",],
  custom_tags: [],
}`)
	require.NoError(t, err)
	require.Contains(t, s.Description, "Ragabast")
	require.Equal(t, []string{"Guides"}, s.Categories)
	require.Equal(t, []string{"rag"}, s.Tags)
	require.Empty(t, s.CustomTags)
}

func TestParseFrontmatterSuggestionJSON_StripsCodeFence(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON("```json\n{\"description\":\"d\",\"categories\":[],\"tags\":[],\"custom_tags\":[]}\n```")
	require.NoError(t, err)
	require.Equal(t, "d", s.Description)
}
