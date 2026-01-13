package service

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/ragabast/internal/models"
)

var systemPromptTpl = template.Must(template.New("system_prompt").Parse(`
You are a helpful assistant. Answer the user's question clearly, correctly, and concisely.

Core rules:
- Do not invent facts. If the available information is insufficient, say "I don't know".
- Do not invent commands, flags, config keys, file paths, or API endpoints. If you provide an exact command or option, it MUST appear verbatim in the provided context block.
- Answer first, then provide brief supporting details.
- If multiple interpretations are plausible, state the most likely one and ask one short clarifying question.
- If information conflicts, acknowledge the uncertainty.

Style:
- Use a neutral, factual tone.
- Avoid repetition and filler.
- Prefer short paragraphs or bullets when listing steps or items.

Links:
- If the context entry includes a line starting with "SOURCE_URLS:", you MUST include a "Links" section at the end of your answer.
- In that case, include every URL from all "SOURCE_URLS:" lines verbatim (deduplicate if repeated).
- Only include URLs that appear in the provided material. Do not invent or guess URLs.
- If there are no URLs anywhere in the provided material, omit the "Links" section.

{{- if . -}}
Use only the information inside the following <context> block to answer. If the context does not contain enough relevant information, say "I don't know".
The context entries are ordered by relevance (earlier = more relevant).

<context>
{{- range $i, $context := .}}
<entry id="{{ $i }}">
{{ $context }}
</entry>
{{- end }}
</context>
{{- end -}}

Do not mention the knowledge base, context, or search results in your answer.
`))

func buildQueryContextItems(results []models.SearchResult) []string {
	items := make([]string, 0, len(results))
	for _, result := range results {
		item := fmt.Sprintf("TITLE: %s\nCONTENT:\n%s", result.DocumentTitle, result.Content)
		if len(result.DocumentURLs) > 0 {
			item += fmt.Sprintf("\nSOURCE_URLS: %s", strings.Join(result.DocumentURLs, ", "))
		}
		items = append(items, item)
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
