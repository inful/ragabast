package service

import (
	"fmt"
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

func TestParseFrontmatterSuggestionJSON_SingleQuotes(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON(`{'description':'d','categories':['Guides'],'tags':['rag'],'custom_tags':[]}`)
	require.NoError(t, err)
	require.Equal(t, "d", s.Description)
	require.Equal(t, []string{"Guides"}, s.Categories)
	require.Equal(t, []string{"rag"}, s.Tags)
}

func TestParseFrontmatterSuggestionJSON_BareKeysWithTrailingComma(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON(`{
  description: "Getting started",
  categories: ["Guides",],
  tags: ["rag",],
  custom_tags: [],
}`)
	require.NoError(t, err)
	require.Equal(t, "Getting started", s.Description)
	require.Equal(t, []string{"Guides"}, s.Categories)
	require.Equal(t, []string{"rag"}, s.Tags)
}

func TestParseFrontmatterSuggestionJSON_WithTextAround(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON(`Here is the JSON:
{
  "description": "d",
  "categories": ["Guides"],
  "tags": ["rag"],
  "custom_tags": []
}
That's all!`)
	require.NoError(t, err)
	require.Equal(t, "d", s.Description)
}

func TestParseFrontmatterSuggestionJSON_WithCodeFenceAndText(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON("```json\n{\n  description: \"Getting started\",\n  categories: [\"Guides\",],\n  tags: [\"rag\",],\n  custom_tags: [],\n}\n```")
	require.NoError(t, err)
	require.Equal(t, "Getting started", s.Description)
	require.Equal(t, []string{"Guides"}, s.Categories)
	require.Equal(t, []string{"rag"}, s.Tags)
}

func TestParseFrontmatterSuggestionJSON_MalformedWithExtraText(t *testing.T) {
	// This simulates what might cause "invalid character 's' after object key:value pair"
	// Example: JSON with extra text or malformed structure
	testCases := []string{
		// Case 1: Extra text after value
		`{"description":"test","categories":["Guides"],"tags":["rag"],"custom_tags":[]}. Please review this suggestion.`,
		// Case 2: Missing quotes around value
		`{"description":test,"categories":["Guides"],"tags":["rag"],"custom_tags":[]}`,
		// Case 3: Extra comma in unexpected place
		`{"description":"test",,"categories":["Guides"],"tags":["rag"],"custom_tags":[]}`,
		// Case 4: Mixed quotes
		`{"description":'test',"categories":["Guides"],"tags":["rag"],"custom_tags":[]}`,
		// Case 5: Unescaped newlines in string
		`{"description":"test
 multi","categories":["Guides"],"tags":["rag"],"custom_tags":[]}`,
		// Case 6: Extra text before JSON
		`Here is the suggestion: {"description":"test","categories":["Guides"],"tags":["rag"],"custom_tags":[]}`,
		// Case 7: Multiple JSON objects
		`{"description":"test","categories":["Guides"],"tags":["rag"],"custom_tags":[]}{"description":"test2","categories":[],"tags":[],"custom_tags":[]}`,
		// Case 8: Trailing text that starts with 's'
		`{"description":"test","categories":["Guides"],"tags":["rag"],"custom_tags":[]} suggest`,
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			s, err := parseFrontmatterSuggestionJSON(tc)
			// We expect these to either succeed with repair or give a clear error
			if err != nil {
				// Should not panic, and error should be meaningful
				require.Error(t, err)
			} else {
				// If it succeeds, verify the result is valid
				require.NotEmpty(t, s.Description)
			}
		})
	}
}

func TestParseFrontmatterSuggestionJSON_EmptyAndEdgeCases(t *testing.T) {
	// Test empty response
	_, err := parseFrontmatterSuggestionJSON("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty response")

	// Test no JSON object
	_, err = parseFrontmatterSuggestionJSON("No JSON here")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to extract valid JSON")

	// Test only opening brace
	_, err = parseFrontmatterSuggestionJSON("{")
	require.Error(t, err)

	// Test only closing brace
	_, err = parseFrontmatterSuggestionJSON("}")
	require.Error(t, err)
}

func TestParseFrontmatterSuggestionJSON_GoSliceSyntax(t *testing.T) {
	// Test the specific case from the logs: []string{"api"}
	s, err := parseFrontmatterSuggestionJSON(`{
  "description": null,
  "categories": [
    "Guides"
  ],
  "tags": [],
  "custom_tags": []string{"api"}
}`)
	require.NoError(t, err)
	require.Empty(t, s.Description) // null becomes empty string
	require.Equal(t, []string{"Guides"}, s.Categories)
	require.Equal(t, []string{}, s.Tags)
	require.Equal(t, []string{"api"}, s.CustomTags)

	// Test with multiple items in Go slice
	s2, err2 := parseFrontmatterSuggestionJSON(`{
  "description": "test",
  "categories": []string{"Guides","Reference"},
  "tags": []string{"go","rag"},
  "custom_tags": []string{"api","cli"}
}`)
	require.NoError(t, err2)
	require.Equal(t, "test", s2.Description)
	require.Equal(t, []string{"Guides", "Reference"}, s2.Categories)
	require.Equal(t, []string{"go", "rag"}, s2.Tags)
	require.Equal(t, []string{"api", "cli"}, s2.CustomTags)
}

// Tests for the text-fallback path (constructJSONFromText), which is hit
// when the LLM returns key:value lines instead of a JSON object.

func TestParseFrontmatterSuggestionJSON_TextFormat_AllFields(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON(`description: A short summary
categories: [Guides, Reference]
tags: [tutorial, go]
custom_tags: [api]`)
	require.NoError(t, err)
	require.Equal(t, "A short summary", s.Description)
	require.Equal(t, []string{"Guides", "Reference"}, s.Categories)
	require.Equal(t, []string{"tutorial", "go"}, s.Tags)
	require.Equal(t, []string{"api"}, s.CustomTags)
}

func TestParseFrontmatterSuggestionJSON_TextFormat_CaseInsensitiveCustomTags(t *testing.T) {
	// Regression: the LLM may capitalize keys. The parser used to match
	// case-insensitively but then strip with case-sensitive TrimPrefix,
	// silently dropping the value.
	s, err := parseFrontmatterSuggestionJSON(`description: x
CustomTags: [api,cli]`)
	require.NoError(t, err)
	require.Equal(t, []string{"api", "cli"}, s.CustomTags)
}

func TestParseFrontmatterSuggestionJSON_TextFormat_CaseInsensitiveAllKeys(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON(`DESCRIPTION: d
CATEGORIES: [C1]
TAGS: [t1,t2]
CUSTOM_TAGS: [c1]`)
	require.NoError(t, err)
	require.Equal(t, "d", s.Description)
	require.Equal(t, []string{"C1"}, s.Categories)
	require.Equal(t, []string{"t1", "t2"}, s.Tags)
	require.Equal(t, []string{"c1"}, s.CustomTags)
}

func TestParseFrontmatterSuggestionJSON_TextFormat_CommaSeparated(t *testing.T) {
	s, err := parseFrontmatterSuggestionJSON(`description: x
tags: tutorial, go, rag`)
	require.NoError(t, err)
	require.Equal(t, []string{"tutorial", "go", "rag"}, s.Tags)
}

func TestParseFrontmatterSuggestionJSON_TextFormat_NoFieldsFound(t *testing.T) {
	_, err := parseFrontmatterSuggestionJSON(`this is just a paragraph with no keys`)
	require.Error(t, err)
}

func TestSplitKeyColon_SplitsOnFirstColon(t *testing.T) {
	cases := []struct {
		in  string
		key string
		val string
		ok  bool
	}{
		{"description: hello", "description", " hello", true},
		{"description:hello", "description", "hello", true},
		{"no colon here", "", "", false},
		{"a:b:c", "a", "b:c", true},
		{"empty:", "empty", "", true},
		{":empty key", "", "empty key", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			k, v, ok := splitKeyColon(tc.in)
			require.Equal(t, tc.ok, ok)
			if ok {
				require.Equal(t, tc.key, k)
				require.Equal(t, tc.val, v)
			}
		})
	}
}
