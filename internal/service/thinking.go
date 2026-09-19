package service

import "strings"

// thinkingStarters are the prefixes that mark a paragraph as
// "the model narrating its reasoning process" rather than "the
// actual answer". LLMs trained with chain-of-thought often leak
// these paragraphs into the user-visible output.
//
// The list is small but each entry was chosen because the
// pattern is almost always meta-commentary when it appears at
// the start of a paragraph:
//   - "Let me / Wait, / Actually, / Hmm, / I need to": the
//     model narrating its own thinking process.
//   - "The user is asking / The question is about": the
//     model paraphrasing the user's question rather than
//     answering it.
//   - "From the context": a meta section header the model
//     uses to introduce a list of sources.
//   - "Looking at / First, let me": meta section starters.
//
// We avoid "Based on the context," and "In summary," because
// those are legitimate real-answer openers — stripping them
// would hide the actual answer.
//
// False-positive risk: a real answer that happens to begin
// with one of these markers will not be stripped. That's worse
// than missing some thinking paragraphs, so we err on the
// side of fewer false positives. If a real answer happens to
// begin with one of these markers, the user sees the preamble
// anyway.
var thinkingStarters = []string{
	"Let me ",
	"Wait,",
	"Actually,",
	"Hmm,",
	"I need to ",
	"First, let me ",
	"Looking at ",
	"The user is asking",
	"From the context",
	"The question is about",
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
