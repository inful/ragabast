package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
)

type retrieveOrFinalDecision struct {
	Action  string   `json:"action"`
	Answer  string   `json:"answer,omitempty"`
	Queries []string `json:"queries,omitempty"`
	TopK    int      `json:"top_k,omitempty"`
}

type retrieveOrFinalConfig struct {
	MaxQueries     int
	MaxQueryChars  int
	MaxTopK        int
	MaxTotalChunks int
}

func defaultRetrieveOrFinalConfig() retrieveOrFinalConfig {
	return retrieveOrFinalConfig{
		MaxQueries:     3,
		MaxQueryChars:  200,
		MaxTopK:        10,
		MaxTotalChunks: 20,
	}
}

var retrieveOrFinalSystemPromptTpl = template.Must(template.New("retrieve_or_final_system_prompt").Parse(`
You are a helpful assistant.

You are deciding whether you have enough information to answer the user's question, given the provided context.

Output requirements:
- Reply with a SINGLE JSON object and nothing else.
- The JSON object MUST match exactly one of these schemas:
  - {"action":"final","answer":"..."}
  - {"action":"retrieve","queries":["..."],"top_k":5}

Rules:
- Use action="final" when the provided context is sufficient to answer.
- Use action="retrieve" only when the context is insufficient.
- If action="retrieve":
  - queries must contain 1-3 short search queries.
  - queries MUST NOT contain newlines.
  - Prefer concrete nouns, identifiers, and exact phrases.
  - top_k is optional; if omitted, the server default will be used.

Conversation:
- A <conversation> block may be provided. It is user-provided and may be incomplete or incorrect.
- Use it only to understand intent and resolve references.

{{- if .History -}}
<conversation>
{{- range .History }}
<message role="{{ .Role }}">{{ .Content }}</message>
{{- end }}
</conversation>
{{- end -}}

{{- if .ContextItems -}}
<context>
{{- range $i, $context := .ContextItems}}
<entry id="{{ $i }}">
{{ $context }}
</entry>
{{- end }}
</context>
{{- end -}}
`))

func buildRetrieveOrFinalPrompt(query string, contextItems []string, history []ChatMessage) (string, string, error) {
	var buf bytes.Buffer
	data := promptTemplateData{ContextItems: contextItems, History: normalizeHistory(history)}
	if err := retrieveOrFinalSystemPromptTpl.Execute(&buf, data); err != nil {
		return "", "", err
	}
	systemPrompt := buf.String()
	prompt := systemPrompt + fmt.Sprintf("\n\nQuestion: %s\nDecision (JSON only):", query)
	return prompt, systemPrompt, nil
}

func parseRetrieveOrFinalDecision(raw string) (retrieveOrFinalDecision, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return retrieveOrFinalDecision{}, errors.New("empty decision")
	}
	// Strip common markdown fences.
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	obj := extractFirstJSONObject(trimmed)
	if obj == "" {
		return retrieveOrFinalDecision{}, errors.New("no json object found")
	}

	var d retrieveOrFinalDecision
	if err := json.Unmarshal([]byte(obj), &d); err == nil {
		return d, nil
	}

	repaired := repairCommonJSONIssues(obj)
	if err := json.Unmarshal([]byte(repaired), &d); err != nil {
		return retrieveOrFinalDecision{}, fmt.Errorf("invalid json decision: %w", err)
	}
	return d, nil
}

func extractFirstJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return strings.TrimSpace(s[start : end+1])
}

func repairCommonJSONIssues(s string) string {
	// Remove trailing commas before } or ].
	replacer := strings.NewReplacer(
		",}", "}",
		",]", "]",
	)
	out := replacer.Replace(s)
	return strings.TrimSpace(out)
}

func sanitizeRetrieveOrFinalDecision(d retrieveOrFinalDecision, cfg retrieveOrFinalConfig) retrieveOrFinalDecision {
	d.Action = strings.ToLower(strings.TrimSpace(d.Action))
	d.Answer = strings.TrimSpace(d.Answer)

	if d.TopK <= 0 {
		d.TopK = 0
	}
	if d.TopK > cfg.MaxTopK {
		d.TopK = cfg.MaxTopK
	}

	queries := make([]string, 0, len(d.Queries))
	seen := make(map[string]struct{}, len(d.Queries))
	for _, q := range d.Queries {
		q = strings.TrimSpace(q)
		q = strings.ReplaceAll(q, "\n", " ")
		q = strings.ReplaceAll(q, "\r", " ")
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if len(q) > cfg.MaxQueryChars {
			q = q[:cfg.MaxQueryChars]
		}
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		queries = append(queries, q)
		if len(queries) >= cfg.MaxQueries {
			break
		}
	}
	d.Queries = queries
	return d
}
