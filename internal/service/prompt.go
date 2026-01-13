package service

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/ragabast/internal/models"
)

var systemPromptTpl = template.Must(template.New("system_prompt").Parse(`
You are a helpful assistant. Answer the user's question clearly, correctly, and concisely.

Core rules:
- Do not invent facts. If the available information is insufficient, say "I don't know".
- Answer first, then provide brief supporting details.
- If multiple interpretations are plausible, state the most likely one and ask one short clarifying question.
- If information conflicts, acknowledge the uncertainty.

Style:
- Use a neutral, factual tone.
- Avoid repetition and filler.
- Prefer short paragraphs or bullets when listing steps or items.

Links:
- If the provided material contains relevant URLs, include them verbatim at the end under a "Links" section.
- Only include URLs that appear in the provided material. Do not invent or guess URLs.
- If there are no relevant URLs, omit the "Links" section.

{{- if . -}}
Use only the information inside the following <context> block to answer. If the context does not contain enough relevant information, say "I don't know".
The bullet points are ordered by relevance (earlier = more relevant).

<context>
{{- range $context := .}}
- {{ $context }}
{{- end }}
</context>
{{- end -}}

Do not mention the knowledge base, context, or search results in your answer.
`))

func buildQueryContextItems(results []models.SearchResult) []string {
	items := make([]string, 0, len(results))
	for _, result := range results {
		items = append(items, fmt.Sprintf("%s: %s", result.DocumentTitle, result.Content))
	}
	return items
}

func buildSystemPrompt(contextItems []string) (string, error) {
	var buf bytes.Buffer
	if err := systemPromptTpl.Execute(&buf, contextItems); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func buildQueryPrompt(query string, contextItems []string) (string, string, error) {
	systemPrompt, err := buildSystemPrompt(contextItems)
	if err != nil {
		return "", "", err
	}

	// Ollama generate uses a single prompt string. We embed the question after the
	// system prompt to mimic a system+user message structure.
	prompt := systemPrompt + fmt.Sprintf("\n\nQuestion: %s\nAnswer:", query)
	return prompt, systemPrompt, nil
}
