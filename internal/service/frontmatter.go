package service

import (
	"context"
	"fmt"
	"log"
	"maps"
	"strings"
)

// FrontmatterSuggestion is the parsed shape the LLM returns from
// SuggestFrontmatter.
type FrontmatterSuggestion struct {
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
	Tags        []string `json:"tags"`
	CustomTags  []string `json:"custom_tags"`
}

// SuggestFrontmatter asks the LLM to propose description, tags,
// and categories for a docbuilder document. The prompt template
// lives in frontmatter_prompt.go; the LLM response parser (which
// tolerates a wide range of malformed JSON, Go slice syntax, and
// markdown code fences) lives in frontmatter_parse.go.
//
// SuggestFrontmatter reuses the LLM chat client constructed once
// at NewService time (s.llmClient) rather than rebuilding one per
// call.
func (s *Service) SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (FrontmatterSuggestion, error) {
	processedContent := truncateForLLM(content)

	prompt, err := buildFrontmatterSuggestPrompt(processedContent, existing, allowedCategories, allowedTags)
	if err != nil {
		return FrontmatterSuggestion{}, err
	}

	log.Printf("FRONTMATTER_SUGGESTION_PROMPT: line_count=%d, content_len=%d, prompt_len=%d",
		strings.Count(processedContent, "\n")+1, len(processedContent), len(prompt))

	options := map[string]any{}
	maps.Copy(options, s.config.Ollama.Options)
	// Deterministic-ish output: frontmatter suggestions should
	// not vary wildly between calls for the same input.
	options["temperature"] = 0.1

	resp, err := s.llmClient.ChatWithSystem(ctx, "", prompt, options)
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

// maxFrontmatterContentLines and maxFrontmatterContentChars bound
// the input we send to the LLM. The model has a limited context
// window, and we want to fail soft (truncate + log) rather than
// hard-fail when a document is too large.
const (
	maxFrontmatterContentLines = 300
	maxFrontmatterContentChars = 15000
)

// truncateForLLM caps content at maxFrontmatterContentLines and
// maxFrontmatterContentChars. Both limits are independent; the
// shorter one wins. Logs a debug line whenever it truncates.
func truncateForLLM(content string) string {
	processed := content

	if lineCount := strings.Count(content, "\n") + 1; lineCount > maxFrontmatterContentLines {
		log.Printf("FRONTMATTER_SUGGESTION_TRUNCATE: content has %d lines, truncating to %d",
			lineCount, maxFrontmatterContentLines)
		lines := strings.Split(content, "\n")
		processed = strings.Join(lines[:maxFrontmatterContentLines], "\n")
	}

	if len(processed) > maxFrontmatterContentChars {
		log.Printf("FRONTMATTER_SUGGESTION_TRUNCATE: content has %d chars, truncating to %d",
			len(processed), maxFrontmatterContentChars)
		processed = processed[:maxFrontmatterContentChars]
	}

	return processed
}
