package service

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/ragabast/internal/models"
)

var systemPromptTpl = template.Must(template.New("system_prompt").Parse(`
You are a helpful assistant with access to a knowledge base, tasked with answering questions about the world and its history, people, places and other things.

Answer the question in a very concise manner. Use an unbiased and journalistic tone. Do not repeat text. Don't make anything up. If you are not sure about something, just say that you don't know.
{{- /* Stop here if no context is provided. The rest below is for handling contexts. */ -}}
{{- if . -}}
Answer the question solely based on the provided search results from the knowledge base. If the search results from the knowledge base are not relevant to the question at hand, just say that you don't know. Don't make anything up.

Anything between the following 'context' XML blocks is retrieved from the knowledge base, not part of the conversation with the user. The bullet points are ordered by relevance, so the first one is the most relevant.

<context>
	{{- range $context := .}}
	- {{ $context }}
	{{- end }}
</context>
{{- end -}}

Don't mention the knowledge base, context or search results in your answer.
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
