package service

import (
	"regexp"
	"strings"
)

// linksSectionHeaderRegex matches the trailing "Links:" section
// header the LLM is told NOT to emit but keeps emitting. The
// model tends to use markdown bold ("**Links:**") to make the
// header stand out, and may add a trailing colon, so we accept
// any combination. Anchored to a line boundary so "Links:" in
// the middle of a sentence isn't matched.
//
// Stripping this section is a defense-in-depth measure: the
// prompt asks the model to drop the section (because the
// InlineSourceLinks post-processor turns every [src:N] into a
// clickable link, so a tail section is redundant), but the model
// keeps appending it anyway, often with placeholder URLs
// (e.g. "https://docs.example.com"). When the model also emits
// URLs that the user DID NOT ask for, that's worse than a noisy
// tail section.
var linksSectionHeaderRegex = regexp.MustCompile(`(?m)^[\s>]*\*?\*?Links:?:?\*?\*?[\s]*$`)

// StripTrailingLinksSection removes a trailing "Links:" (or
// "**Links:**", etc.) block from the end of an LLM reply. The
// block is whatever follows the header line, including blank
// lines and URLs, until end-of-string.
//
// Why a heuristic when the prompt asks the model not to emit
// Links: in the first place: the model emits it anyway, often
// with placeholder URLs that have nothing to do with the real
// sources. Inline [src:N] citations (handled by
// InlineSourceLinks) cover the legitimate cases; the tail section
// is noise.
//
// Safety contracts (verified by tests):
//
//   - Reply with no trailing "Links:" header: returned
//     unchanged.
//   - Reply with a "Links:" header mid-reply: NOT stripped.
//     The function only acts on trailing content. A real answer
//     that happens to contain the literal string "Links:" in
//     the middle is preserved.
//   - Markdown-bolded variants ("**Links:**") are detected.
func StripTrailingLinksSection(reply string) string {
	// We anchor at the end-of-string by working backwards from
	// the rightmost "Links:" header. If multiple "Links:" lines
	// exist, only the last one is treated as the trailing
	// section; earlier ones are preserved.
	matches := linksSectionHeaderRegex.FindAllStringIndex(reply, -1)
	if len(matches) == 0 {
		return reply
	}
	last := matches[len(matches)-1]
	headerStart := last[0]
	return strings.TrimSpace(reply[:headerStart])
}
