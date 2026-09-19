package service

import (
	"bytes"
	"encoding/json"
	"strings"
	"text/template"
)

// frontmatterSuggestPromptData is the template binding for the
// frontmatter-suggest prompt.
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

// buildFrontmatterSuggestPrompt renders the prompt template with
// the supplied context. Errors only on template parse or
// execution problems; data-shape problems show up later when the
// LLM response is parsed.
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

	if existing == nil {
		existing = map[string]any{}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, frontmatterSuggestPromptData{
		AllowedCategories: allowedCategories,
		AllowedTags:       allowedTags,
		Existing:          existing,
		Content:           content,
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
