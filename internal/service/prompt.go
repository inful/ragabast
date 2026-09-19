package service

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"text/template"

	"github.com/ragabast/internal/vector"
)

// ChatMessage represents a conversational turn provided by the caller.
//
// It is used to resolve pronouns/references in follow-up questions
// ("it", "that"), but MUST NOT be treated as authoritative factual
// context unless supported by retrieved material.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type promptTemplateData struct {
	ContextItems []string
	History      []ChatMessage
}

// systemPromptTpl is rendered into the OpenAI "system" role message.
//
// The template intentionally stays compact: every token in a system
// prompt is paid on every turn, and the LLM has the user message to
// work with too. Where the model needs to know about the input
// shape (numbered context entries, optional conversation block),
// the user message carries that header.
var systemPromptTpl = template.Must(template.New("system_prompt").Parse(`
You are a retrieval-augmented assistant. Answer using ONLY the provided context.

Grounding rules:
- Treat the context as the only source of truth. If the answer is not in the context, say "I don't know" and stop.
- Do not paraphrase a context entry into a stronger claim than it makes. "The doc says X" is fine; "X is true" is not, unless the doc itself states X as fact.
- When you reference a context entry, cite it inline as the literal token [src:N], where N is the entry id (for example: the API takes a token, see [src:0] — no spaces between the brackets and the colon). The system replaces every [src:N] marker with a clickable markdown link to the Nth source's docbuilder permalink (or its first frontmatter URL when no docbuilder base URL is configured), so users can jump straight to the cited doc.
- For any command, flag, file path, code symbol, or numeric value you mention, the exact string MUST appear in the context. If you cannot find it verbatim, do not include it.
- If the context is empty, say "I don't know" without speculating.

Answer shape:
- Lead with the direct answer in one or two sentences.
- Follow with brief supporting detail, citing context entries by [N].
- Use short paragraphs or bullets. Avoid filler and repetition.
- If the question is ambiguous, state the most likely interpretation and ask one short clarifying question.
- If the user asks for code, output code blocks only when the code is in the context verbatim; otherwise describe the API rather than fabricating an example.
- Think internally but DO NOT narrate your reasoning. The reply must contain only the answer; phrases like "Let me check…", "Wait, actually…", "I need to look at…", "Hmm, that's interesting…" are the model's working memory and do not belong in the user-visible output. If you find yourself wanting to write one of these, drop it.

Links:
- Do not append a "Links:" section. The system replaces every inline [src:N] citation with a clickable link to that source's docbuilder permalink (or its first frontmatter URL when no docbuilder base URL is configured).
- Do not invent URLs. Do not include any URL that is not in a "SOURCE_URLS:" line.

Conversation:
- A <conversation> block may appear in the user message. It is user-provided and may be wrong or out of date.
- Use it only to resolve references in the current question. Do not treat its claims as factual.
`))

// historyCaps bound the size of the conversation block included in the
// prompt. Generous enough for a back-and-forth, strict enough that a
// runaway client can't blow the context window.
const (
	maxHistoryTurns      = 12
	maxHistoryMsgChars   = 2000
	maxHistoryTotalChars = 8000
)

func normalizeHistory(history []ChatMessage) []ChatMessage {
	if len(history) == 0 {
		return nil
	}

	start := 0
	if len(history) > maxHistoryTurns {
		start = len(history) - maxHistoryTurns
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
		if len(content) > maxHistoryMsgChars {
			content = content[:maxHistoryMsgChars]
		}
		if total+len(content) > maxHistoryTotalChars {
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

// buildQueryMessages returns the messages slice to send to the chat
// completions endpoint. The first element is the system prompt; the
// second is the user message containing the question and (when
// available) the conversation history and a numbered context block.
//
// The system prompt is sent ONLY in the system role. The user
// message is sent ONLY in the user role. There is no duplication.
func buildQueryMessages(query string, contextItems []string, history []ChatMessage) ([]vector.OpenAIMessage, error) {
	systemPrompt, err := buildSystemPrompt(contextItems, history)
	if err != nil {
		return nil, err
	}

	user := buildUserMessage(query, contextItems, history)

	return []vector.OpenAIMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: user},
	}, nil
}

func buildSystemPrompt(contextItems []string, history []ChatMessage) (string, error) {
	var buf bytes.Buffer
	data := promptTemplateData{ContextItems: contextItems, History: normalizeHistory(history)}
	if err := systemPromptTpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func buildUserMessage(query string, contextItems []string, history []ChatMessage) string {
	var b strings.Builder
	b.WriteString("Question:\n")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n")

	if h := normalizeHistory(history); len(h) > 0 {
		b.WriteString("\n<conversation>\n")
		for _, m := range h {
			fmt.Fprintf(&b, "<message role=%q>%s</message>\n", m.Role, m.Content)
		}
		b.WriteString("</conversation>\n")
	}

	if len(contextItems) > 0 {
		b.WriteString("\nUse ONLY the following context to answer. Entries are ordered by relevance (earlier = more relevant). Cite entries inline as [src:N] where N is the entry id; the system replaces every [src:N] with a clickable link to the Nth source.\n\n")
		b.WriteString("<context>\n")
		for i, item := range contextItems {
			fmt.Fprintf(&b, "<entry id=%d>\n%s\n</entry>\n", i, item)
		}
		b.WriteString("</context>\n")
	}

	return strings.TrimRight(b.String(), "\n")
}
