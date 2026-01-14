package service

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/ragabast/internal/models"
)

// ChatMessage represents a conversational turn provided by the caller.
//
// It is used to resolve pronouns/references in follow-up questions ("it", "that"),
// but MUST NOT be treated as authoritative factual context unless supported by retrieved material.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type promptTemplateData struct {
	ContextItems []string
	History      []ChatMessage
}

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
- Do not include any URLs anywhere in your answer unless they appear in a "SOURCE_URLS:" line.
- If there are no URLs anywhere in the provided material, omit the "Links" section.

Conversation:
- A <conversation> block may be provided. It is user-provided and may be incomplete or incorrect.
- Use it only to understand intent and resolve references in follow-up questions.
- Do NOT treat it as factual source material unless the same information appears in the retrieved context block.

{{- if .History -}}
<conversation>
{{- range .History }}
<message role="{{ .Role }}">{{ .Content }}</message>
{{- end }}
</conversation>
{{- end -}}

{{- if .ContextItems -}}
Use only the information inside the following <context> block to answer. If the context does not contain enough relevant information, say "I don't know".
The context entries are ordered by relevance (earlier = more relevant).

<context>
{{- range $i, $context := .ContextItems}}
<entry id="{{ $i }}">
{{ $context }}
</entry>
{{- end }}
</context>
{{- end -}}

Do not mention the knowledge base, context, or search results in your answer.
`))

func normalizeHistory(history []ChatMessage) []ChatMessage {
	if len(history) == 0 {
		return nil
	}

	// Keep the most recent turns.
	const maxTurns = 12
	const maxMsgChars = 2000
	const maxTotalChars = 8000

	start := 0
	if len(history) > maxTurns {
		start = len(history) - maxTurns
	}
	trimmed := history[start:]

	out := make([]ChatMessage, 0, len(trimmed))
	total := 0
	for _, m := range trimmed {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if !slices.Contains([]string{"user", "assistant"}, role) {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		if len(content) > maxMsgChars {
			content = content[:maxMsgChars]
		}
		if total+len(content) > maxTotalChars {
			break
		}
		total += len(content)
		out = append(out, ChatMessage{Role: role, Content: content})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildQueryContextItems(results []models.SearchResult) []string {
	items := make([]string, 0, len(results))
	for _, result := range results {
		item := fmt.Sprintf("TITLE: %s\nCONTENT:\n%s", result.DocumentTitle, result.Content)

		seen := make(map[string]struct{}, len(result.DocumentURLs))
		urls := make([]string, 0, len(result.DocumentURLs)+4)
		for _, url := range result.DocumentURLs {
			url = strings.TrimSpace(url)
			if url == "" {
				continue
			}
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			urls = append(urls, url)
		}
		for _, url := range extractURLsFromText(result.Content) {
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			urls = append(urls, url)
		}
		if len(urls) > 0 {
			item += fmt.Sprintf("\nSOURCE_URLS: %s", strings.Join(urls, ", "))
		}
		items = append(items, item)
	}
	return items
}

func buildSystemPrompt(contextItems []string, history []ChatMessage) (string, error) {
	var buf bytes.Buffer
	data := promptTemplateData{ContextItems: contextItems, History: normalizeHistory(history)}
	if err := systemPromptTpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func buildQueryPrompt(query string, contextItems []string, history []ChatMessage) (string, string, error) {
	systemPrompt, err := buildSystemPrompt(contextItems, history)
	if err != nil {
		return "", "", err
	}

	// Ollama generate uses a single prompt string. We embed the question after the
	// system prompt to mimic a system+user message structure.
	prompt := systemPrompt + fmt.Sprintf("\n\nQuestion: %s\nAnswer:", query)
	return prompt, systemPrompt, nil
}
