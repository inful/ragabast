package service

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"text/template"

	"github.com/ragabast/internal/models"
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
- When you reference a context entry, cite it inline as [N], where N is the entry id (e.g. "the API takes a token [1]").
- For any command, flag, file path, code symbol, or numeric value you mention, the exact string MUST appear in the context. If you cannot find it verbatim, do not include it.
- If the context is empty, say "I don't know" without speculating.

Answer shape:
- Lead with the direct answer in one or two sentences.
- Follow with brief supporting detail, citing context entries by [N].
- Use short paragraphs or bullets. Avoid filler and repetition.
- If the question is ambiguous, state the most likely interpretation and ask one short clarifying question.
- If the user asks for code, output code blocks only when the code is in the context verbatim; otherwise describe the API rather than fabricating an example.

Links:
- If any context entry contains a "SOURCE_URLS:" line, end your answer with a single "Links:" section that lists every URL from every "SOURCE_URLS:" line, deduplicated, exactly as written.
- Do not invent URLs. Do not include any URL that is not in a "SOURCE_URLS:" line.
- If no context entry has "SOURCE_URLS:", omit the "Links:" section entirely.

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

// ChunkFetcher returns a parent chunk's body given its ID, or
// (nil, false) if the chunk is not available. It is the seam
// between the prompt builder and the vector store: tests pass a
// stub; production passes vectorChunkFetcher below.
type ChunkFetcher interface {
	FetchChunk(ctx context.Context, id string) (*models.Chunk, bool, error)
}

// vectorChunkFetcher adapts the Service's GetChunk method to the
// ChunkFetcher interface above, so the prompt builder can pull
// parent context without depending on the vector package directly.
// Lives here (not in service.go) because it's only consumed by
// the prompt-building code path.
type vectorChunkFetcher struct {
	s *Service
}

func (v vectorChunkFetcher) FetchChunk(ctx context.Context, id string) (*models.Chunk, bool, error) {
	chunk, err := v.s.GetChunk(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if chunk == nil {
		return nil, false, nil
	}
	return chunk, true, nil
}

// buildQueryContextItems formats the retrieved chunks for the LLM.
//
// Each item is a small block containing:
//   - the parent document's title
//   - the chunk's section path (HeaderPath)
//   - the document's tags and categories, when present
//   - the parent chunk's body (when this is a child chunk and the
//     parent is not already represented in the result set), so the
//     LLM has the section header and the lead-in context
//   - the chunk's own body
//   - the SOURCE_URLS line, when the document has any
//
// The model cites entries by index.
func buildQueryContextItems(ctx context.Context, results []models.SearchResult, fetcher ChunkFetcher) []string {
	// Pre-resolve which parent chunks are already in the result set
	// so we don't prepend the same parent body to multiple siblings.
	presentChunkIDs := make(map[string]struct{}, len(results))
	for _, r := range results {
		if r.ChunkID != "" {
			presentChunkIDs[r.ChunkID] = struct{}{}
		}
	}

	items := make([]string, 0, len(results))
	for _, result := range results {
		items = append(items, formatContextEntry(ctx, result, presentChunkIDs, fetcher))
	}
	return items
}

// resolveParentContext returns the trimmed body of the parent chunk
// when one should be prepended to the result, or "" if no parent
// applies (root chunk, parent already in result set, missing chunk,
// fetch error, or empty body). Errors are logged but do not block
// prompt building.
func resolveParentContext(ctx context.Context, result models.SearchResult, presentChunkIDs map[string]struct{}, fetcher ChunkFetcher) string {
	if result.ParentID == "" {
		return ""
	}
	if _, already := presentChunkIDs[result.ParentID]; already {
		return ""
	}
	if fetcher == nil {
		return ""
	}
	parent, ok, err := fetcher.FetchChunk(ctx, result.ParentID)
	if err != nil {
		log.Printf("prompt: fetch parent %q for chunk %q: %v", result.ParentID, result.ChunkID, err)
		return ""
	}
	if !ok || parent == nil {
		return ""
	}
	return strings.TrimSpace(parent.Content)
}

func formatContextEntry(ctx context.Context, result models.SearchResult, presentChunkIDs map[string]struct{}, fetcher ChunkFetcher) string {
	var b strings.Builder
	b.WriteString("TITLE: ")
	b.WriteString(strings.TrimSpace(result.DocumentTitle))
	b.WriteByte('\n')

	if section := strings.TrimSpace(result.HeaderPath); section != "" {
		b.WriteString("SECTION: ")
		b.WriteString(section)
		b.WriteByte('\n')
	}

	if len(result.DocumentTags) > 0 {
		b.WriteString("TAGS: ")
		b.WriteString(strings.Join(result.DocumentTags, ", "))
		b.WriteByte('\n')
	}
	if len(result.DocumentCategories) > 0 {
		b.WriteString("CATEGORIES: ")
		b.WriteString(strings.Join(result.DocumentCategories, ", "))
		b.WriteByte('\n')
	}

	// Prepend parent context when this is a child chunk and the
	// parent is not already present in the result set. A missing
	// or unreadable parent is logged and skipped: the chunk itself
	// is still useful on its own.
	if parentBody := resolveParentContext(ctx, result, presentChunkIDs, fetcher); parentBody != "" {
		b.WriteString("PARENT_CONTEXT:\n")
		b.WriteString(parentBody)
		b.WriteByte('\n')
	}

	b.WriteString("CONTENT:\n")
	b.WriteString(result.Content)

	if urls := collectContextURLs(result); len(urls) > 0 {
		b.WriteString("\nSOURCE_URLS: ")
		b.WriteString(strings.Join(urls, ", "))
	}
	return b.String()
}

// collectContextURLs returns the URLs for a single result, deduped,
// with the document's declared URLs first and any URLs found inside
// the chunk body appended.
func collectContextURLs(result models.SearchResult) []string {
	seen := make(map[string]struct{}, len(result.DocumentURLs)+2)
	urls := make([]string, 0, len(result.DocumentURLs)+2)
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
	return urls
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
		b.WriteString("\nUse ONLY the following context to answer. Entries are ordered by relevance (earlier = more relevant). Cite entries inline as [N] where N is the entry id.\n\n")
		b.WriteString("<context>\n")
		for i, item := range contextItems {
			fmt.Fprintf(&b, "<entry id=%d>\n%s\n</entry>\n", i, item)
		}
		b.WriteString("</context>\n")
	}

	return strings.TrimRight(b.String(), "\n")
}
