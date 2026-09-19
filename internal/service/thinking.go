package service

import "strings"

// thinkingStarters are the prefixes that mark a paragraph as
// "the model narrating its reasoning process" rather than "the
// actual answer". LLMs trained with chain-of-thought often leak
// these paragraphs into the user-visible output.
//
// The list is intentionally small. Adding more starters risks
// false positives (a real answer that happens to start with
// one of these). If a real answer happens to start with one
// of these markers, the user's first-paragraph will not be
// stripped — they'll see the "Let me X" preamble. That's worse
// than missing some thinking paragraphs. Err on the side of
// fewer false positives.
//
// The leading-space form ("Let me ") matches when the next
// character is part of a sentence. The comma form ("Wait,") is
// unambiguous because it requires a comma right after the word.
var thinkingStarters = []string{
	"Let me ",
	"Wait,",
	"Actually,",
	"Hmm,",
	"I need to ",
	"First, let me ",
	"Looking at ",
}

// StripLeadingThinking removes leading "the model is thinking"
// paragraphs from an LLM reply. A paragraph is considered
// thinking when its first non-whitespace line starts with one
// of thinkingStarters. The function stops stripping the moment
// it finds a paragraph whose first line does NOT match, so the
// real answer is preserved verbatim.
//
// Why per-paragraph: thinking blocks in the LLM output are
// typically separated from the real answer by a blank line. We
// split on blank lines rather than line-by-line so multi-line
// thinking paragraphs (which models love to write) are removed
// as a unit, not line by line.
//
// Why leading only: a model that starts cleanly and then
// re-corrects itself later ("Wait, let me reconsider this")
// is doing useful self-correction; stripping that would hide a
// genuine revision. The fix targets only the case where the
// model emits its reasoning process BEFORE the actual answer.
func StripLeadingThinking(reply string) string {
	// Split into paragraphs on blank lines. The regex would be
	// shorter; strings.Split keeps the dependency surface flat
	// for a 30-line helper.
	paragraphs := strings.Split(reply, "\n\n")
	if len(paragraphs) <= 1 {
		return strings.TrimSpace(reply)
	}

	firstNonThinking := 0
	for i, p := range paragraphs {
		if !isParagraphThinking(p) {
			firstNonThinking = i
			break
		}
		// If every paragraph is thinking, we don't want to
		// return an empty string — keep at least the last
		// paragraph.
		if i == len(paragraphs)-1 {
			firstNonThinking = i
		}
	}

	if firstNonThinking == 0 {
		return strings.TrimSpace(reply)
	}
	return strings.TrimSpace(strings.Join(paragraphs[firstNonThinking:], "\n\n"))
}

// paragraphStartsWithThinking reports whether the first
// non-whitespace line of the paragraph begins with a known
// thinking-starter token.
func isParagraphThinking(p string) bool {
	for line := range strings.SplitSeq(p, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, starter := range thinkingStarters {
			if strings.HasPrefix(trimmed, starter) {
				return true
			}
		}
		return false
	}
	// All-blank paragraph — treat as not thinking. This shouldn't
	// happen because we split on blank lines, but if it does,
	// don't strip it.
	return false
}
