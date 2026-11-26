package vector

import (
	"strings"
)

// docTitlePlaceholder is the token an operator puts in the doc
// prompt where the document's title belongs.
//
// Models that take a task instruction (Google's EmbeddingGemma
// family, jina v5) format a document as
// `title: {title} | text: {content}` — the title tells the model
// what the chunk is *about*, which the chunk body alone often does
// not. A doc prompt is otherwise a static string, so without a
// placeholder the title could not be included at all.
const docTitlePlaceholder = "{title}"

// noDocTitle is what the placeholder becomes when the document has
// no title. It matches the EmbeddingGemma convention: the model
// card says to use `title: none` when no title is available.
const noDocTitle = "none"

// applyDocPrompt prepends the doc prompt to text, substituting
// {title} with the document's title.
//
// A prompt without the placeholder is used verbatim, so models that
// take no task input keep working unchanged. A prompt with the
// placeholder and a title-less document renders `title: none`,
// which is exactly what the EmbeddingGemma model card prescribes
// for that case.
func applyDocPrompt(prompt, title, text string) string {
	if prompt == "" {
		return text
	}
	if !strings.Contains(prompt, docTitlePlaceholder) {
		return prompt + text
	}
	if strings.TrimSpace(title) == "" {
		title = noDocTitle
	}
	return strings.ReplaceAll(prompt, docTitlePlaceholder, title) + text
}
