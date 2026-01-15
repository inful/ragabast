package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"regexp"
	"strings"
	"text/template"

	"github.com/ragabast/internal/vector"
)

type FrontmatterSuggestion struct {
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
	Tags        []string `json:"tags"`
	CustomTags  []string `json:"custom_tags"`
}

type frontmatterSuggestPromptData struct {
	AllowedCategories []string
	AllowedTags       []string
	Existing          map[string]any
	Content           string
}

const frontmatterSuggestPromptText = `You are helping update YAML frontmatter for a documentation page.

Rules:
- ONLY use the provided document content. Do NOT use outside knowledge.
- You must NOT modify existing frontmatter keys other than proposing values for: description, tags, categories.
- Description: one short sentence (max ~180 chars) summarizing the document.
- Categories: MUST be chosen ONLY from the allowed categories list. Suggest 1-3 categories when possible.
- Tags: Prefer choosing from allowed tags. Suggest 3-8 tags when possible.
- Tags not in the allowed list MUST be returned in custom_tags (not in tags).
- Tags should be lowercase.
- Categories must match the allowed categories exactly (including capitalization).
- Keep existing tags/categories intact: do not remove or rename them.
- Even if existing frontmatter already includes tags/categories, still suggest additional ones that fit.
- Output MUST be valid JSON and MUST contain ONLY these keys: description, categories, tags, custom_tags.
- description may be an empty string if the document already has a description.
- CRITICAL: Do NOT wrap the JSON in markdown code fences. Return ONLY the raw JSON object.

Allowed categories:
{{range .AllowedCategories}}- {{.}}
{{end}}

Allowed tags:
{{range .AllowedTags}}- {{.}}
{{end}}

Existing frontmatter (JSON):
{{.Existing | toJSON}}

Document content:
"""
{{.Content}}
"""

Return ONLY the raw JSON object now.`

func buildFrontmatterSuggestPrompt(content string, existing map[string]any, allowedCategories []string, allowedTags []string) (string, error) {
	funcMap := template.FuncMap{
		"toJSON": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "{}", err
			}
			return string(b), nil
		},
	}

	tmpl, err := template.New("frontmatter_suggest").Funcs(funcMap).Parse(strings.TrimSpace(frontmatterSuggestPromptText))
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if existing == nil {
		existing = map[string]any{}
	}
	err = tmpl.Execute(&buf, frontmatterSuggestPromptData{
		AllowedCategories: allowedCategories,
		AllowedTags:       allowedTags,
		Existing:          existing,
		Content:           content,
	})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func stripJSONCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "```") {
		return s
	}
	parts := strings.Split(s, "```")

	// Handle edge case where response is just "```" or has no content between fences
	if len(parts) < 2 {
		return s
	}

	// Find the first non-empty part that could contain JSON
	var fenced string
	for i := 1; i < len(parts); i++ {
		fenced = strings.TrimSpace(parts[i])
		if fenced != "" {
			break
		}
	}

	// If all parts are empty, return original
	if fenced == "" {
		return s
	}

	// Check if it starts with a language identifier like "json"
	if idx := strings.Index(fenced, "\n"); idx > 0 {
		first := strings.TrimSpace(fenced[:idx])
		if first == "json" {
			return strings.TrimSpace(fenced[idx+1:])
		}
	}

	return fenced
}

func parseFrontmatterSuggestionJSON(raw string) (FrontmatterSuggestion, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return FrontmatterSuggestion{}, errors.New("empty response")
	}

	// Strip common markdown code fences.
	s = stripJSONCodeFence(s)

	// Try to find and extract JSON object
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')

	if start != -1 && end != -1 && end > start {
		// Found braces, extract and parse
		jsonStr := s[start : end+1]

		var out FrontmatterSuggestion
		if err := json.Unmarshal([]byte(jsonStr), &out); err == nil {
			return out, nil
		}

		// Try repairs
		repaired := repairJSONObjectLike(jsonStr)
		if err := json.Unmarshal([]byte(repaired), &out); err == nil {
			return out, nil
		}

		aggressiveRepair := aggressiveRepairJSON(jsonStr)
		if err := json.Unmarshal([]byte(aggressiveRepair), &out); err == nil {
			return out, nil
		}
	}

	// Fallback: try to construct JSON from LLM response that might contain key-value pairs
	// This handles cases where LLM returns text like: "description: ... tags: [...]"
	constructedJSON := constructJSONFromText(s)
	if constructedJSON != "" {
		var out FrontmatterSuggestion
		if err := json.Unmarshal([]byte(constructedJSON), &out); err == nil {
			return out, nil
		}
	}

	// Log the LLM response for debugging when parsing fails
	log.Printf("FRONTMATTER_SUGGESTION_PARSE_ERROR: Failed to parse LLM response as JSON")
	log.Printf("FRONTMATTER_SUGGESTION_RAW: %q", raw)
	log.Printf("FRONTMATTER_SUGGESTION_PROCESSED: %q", s)
	log.Printf("FRONTMATTER_SUGGESTION_CONSTRUCTED: %q", constructedJSON)

	return FrontmatterSuggestion{}, errors.New("failed to extract valid JSON from LLM response")
}

// aggressiveRepairJSON handles more complex malformed JSON cases.
func aggressiveRepairJSON(s string) string {
	// Remove any text before the first { and after the last }
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start != -1 && end != -1 && end > start {
		s = s[start : end+1]
	}

	// Apply all repair strategies
	s = repairJSONObjectLike(s)

	// Fix Go slice syntax to JSON array syntax: []string{"a", "b"} -> ["a", "b"]
	// This handles the specific case where LLMs output Go struct literals
	goSliceRe := regexp.MustCompile(`\[\]string\{([^}]*)\}`)
	s = goSliceRe.ReplaceAllString(s, `[$1]`)

	// Clean up any remaining Go syntax - remove quotes around items that might be duplicated
	// Pattern: ["item1", "item2"] might become ["item1", "item2"] after the above replacement
	// But we need to ensure proper JSON formatting
	s = strings.ReplaceAll(s, `""`, `"`)

	// Handle common LLM response patterns that might include extra text
	// Remove lines that don't look like JSON
	lines := strings.Split(s, "\n")
	var jsonLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Keep lines that look like JSON (contain quotes, colons, brackets, or are valid JSON values)
		if strings.Contains(trimmed, `"`) ||
			strings.Contains(trimmed, `:`) ||
			strings.Contains(trimmed, `[`) ||
			strings.Contains(trimmed, `]`) ||
			strings.Contains(trimmed, `{`) ||
			strings.Contains(trimmed, `}`) ||
			trimmed == "true" || trimmed == "false" || trimmed == "null" {
			jsonLines = append(jsonLines, line)
		}
	}

	return strings.Join(jsonLines, "\n")
}

var (
	jsonBareKeyRe   = regexp.MustCompile(`([\{,]\s*)([A-Za-z_][A-Za-z0-9_]*)(\s*:)`)
	jsonTrailingCom = regexp.MustCompile(`,\s*([\}\]])`)
	jsonSingleQuote = regexp.MustCompile(`'([^']*)'`)
)

func repairJSONObjectLike(s string) string {
	// First, replace single quotes with double quotes (but be careful not to break escaped quotes)
	out := jsonSingleQuote.ReplaceAllString(s, `"$1"`)

	// Quote bare keys: {foo: "bar"} -> {"foo": "bar"}
	out = jsonBareKeyRe.ReplaceAllString(out, `$1"$2"$3`)

	// Remove trailing commas before } or ]
	out = jsonTrailingCom.ReplaceAllString(out, `$1`)

	// Handle unquoted boolean/null values
	out = strings.ReplaceAll(out, `: true`, `: true`)
	out = strings.ReplaceAll(out, `: false`, `: false`)
	out = strings.ReplaceAll(out, `: null`, `: null`)

	return out
}

// constructJSONFromText attempts to construct valid JSON from plain text that may contain
// key-value pairs. This handles cases where the LLM returns text like:
// "description: Some text\ntags: [tag1, tag2]\ncategories: [cat1]".
func constructJSONFromText(text string) string {
	// Look for the four expected fields
	description := ""
	categories := []string{}
	tags := []string{}
	customTags := []string{}

	lines := strings.SplitSeq(text, "\n")
	for line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for description field
		if strings.HasPrefix(strings.ToLower(line), "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			continue
		}

		// Check for categories field
		if strings.HasPrefix(strings.ToLower(line), "categories:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "categories:"))
			categories = parseArrayValue(val)
			continue
		}

		// Check for tags field
		if strings.HasPrefix(strings.ToLower(line), "tags:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
			tags = parseArrayValue(val)
			continue
		}

		// Check for custom_tags field
		if strings.HasPrefix(strings.ToLower(line), "custom_tags:") || strings.HasPrefix(strings.ToLower(line), "customtags:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "custom_tags:"))
			val = strings.TrimSpace(strings.TrimPrefix(val, "customtags:"))
			customTags = parseArrayValue(val)
			continue
		}
	}

	// Only construct JSON if we found at least one field
	if description == "" && len(categories) == 0 && len(tags) == 0 && len(customTags) == 0 {
		return ""
	}

	// Build the JSON object
	result := map[string]any{
		"description": description,
		"categories":  categories,
		"tags":        tags,
		"custom_tags": customTags,
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return ""
	}

	return string(jsonBytes)
}

// parseArrayValue extracts string values from common array formats.
func parseArrayValue(val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return []string{}
	}

	// Handle [item1, item2, item3] format
	if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
		val = strings.TrimPrefix(val, "[")
		val = strings.TrimSuffix(val, "]")
		parts := strings.Split(val, ",")
		result := []string{}
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			// Remove quotes if present
			trimmed = strings.Trim(trimmed, `"'`)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}

	// Handle comma-separated values
	parts := strings.Split(val, ",")
	result := []string{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		trimmed = strings.Trim(trimmed, `"'`)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func (s *Service) SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (FrontmatterSuggestion, error) {
	// Truncate content if it's too large to prevent LLM issues
	// Limit to ~300 lines or ~15,000 characters to stay well within typical LLM context limits
	// The gemma:2b model has limited context window, so we need to be conservative
	maxLines := 300
	maxChars := 15000

	lineCount := strings.Count(content, "\n") + 1
	processedContent := content

	if lineCount > maxLines {
		log.Printf("FRONTMATTER_SUGGESTION_TRUNCATE: content has %d lines, truncating to %d", lineCount, maxLines)
		lines := strings.Split(content, "\n")
		processedContent = strings.Join(lines[:maxLines], "\n")
	}

	if len(processedContent) > maxChars {
		log.Printf("FRONTMATTER_SUGGESTION_TRUNCATE: content has %d chars, truncating to %d", len(processedContent), maxChars)
		processedContent = processedContent[:maxChars]
	}

	prompt, err := buildFrontmatterSuggestPrompt(processedContent, existing, allowedCategories, allowedTags)
	if err != nil {
		return FrontmatterSuggestion{}, err
	}

	// Log prompt size for debugging
	promptSize := len(prompt)
	log.Printf("FRONTMATTER_SUGGESTION_PROMPT: line_count=%d, content_len=%d, prompt_len=%d", strings.Count(processedContent, "\n")+1, len(processedContent), promptSize)

	llmClient := vector.NewOllamaLLMClientWithTimeout(s.config.Ollama.BaseURL, s.config.Ollama.GenerationModel, s.config.Ollama.Timeout)

	options := map[string]any{}
	maps.Copy(options, s.config.Ollama.Options)
	// Make output more deterministic.
	options["temperature"] = 0.1

	resp, err := llmClient.GenerateWithOptions(ctx, prompt, options)
	if err != nil {
		return FrontmatterSuggestion{}, fmt.Errorf("LLM generation failed: %w", err)
	}

	log.Printf("FRONTMATTER_SUGGESTION_RESPONSE_LEN: %d", len(resp))
	log.Printf("FRONTMATTER_SUGGESTION_RESPONSE_CONTENT: %q", resp)

	sug, err := parseFrontmatterSuggestionJSON(resp)
	if err != nil {
		return FrontmatterSuggestion{}, err
	}

	return sug, nil
}
