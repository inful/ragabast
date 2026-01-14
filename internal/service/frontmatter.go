package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
- Do NOT retrieve or infer anything from any database or vector store.
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

Return JSON now.`

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
	if !strings.Contains(s, "```") {
		return s
	}
	parts := strings.Split(s, "```")
	if len(parts) < 2 {
		return s
	}
	fenced := strings.TrimSpace(parts[1])
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

	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start == -1 || end == -1 || end <= start {
		return FrontmatterSuggestion{}, errors.New("no JSON object found")
	}
	s = s[start : end+1]

	var out FrontmatterSuggestion
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return FrontmatterSuggestion{}, fmt.Errorf("invalid JSON: %w", err)
	}
	return out, nil
}

func (s *Service) SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (FrontmatterSuggestion, error) {
	prompt, err := buildFrontmatterSuggestPrompt(content, existing, allowedCategories, allowedTags)
	if err != nil {
		return FrontmatterSuggestion{}, err
	}

	llmClient := vector.NewOllamaLLMClientWithTimeout(s.config.Ollama.BaseURL, s.config.Ollama.GenerationModel, s.config.Ollama.Timeout)

	options := map[string]any{}
	maps.Copy(options, s.config.Ollama.Options)
	// Make output more deterministic.
	options["temperature"] = 0.0

	resp, err := llmClient.GenerateWithOptions(ctx, prompt, options)
	if err != nil {
		return FrontmatterSuggestion{}, fmt.Errorf("LLM generation failed: %w", err)
	}

	sug, err := parseFrontmatterSuggestionJSON(resp)
	if err != nil {
		return FrontmatterSuggestion{}, err
	}

	return sug, nil
}
