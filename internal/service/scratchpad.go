package service

import "strings"

// scratchpadOpen and scratchpadClose are the canonical
// delimiters the system prompt requires the LLM to wrap its
// reasoning in. The model is told: put your reasoning inside
// <scratchpad>...</scratchpad>, then write the user-visible
// answer after. This shifts the boundary-marking burden from
// "heuristic regex on prose" (fragile) to "model emits a
// structural delimiter" (deterministic).
const (
	scratchpadOpen  = "<scratchpad>"
	scratchpadClose = "</scratchpad>"
)

// StripScratchpad removes the <scratchpad>...</scratchpad> block
// from the LLM reply and returns everything after it. Used as the
// primary defense against reasoning leakage: the model is told to
// put its working memory inside a scratchpad block, and we strip
// that block deterministically before further post-processing.
//
// Safety contracts (verified by the tests):
//
//   - Reply with no scratchpad tags: returned unchanged.
//     A model that emits no preamble at all (or only preamble
//     outside the format) gets a clean passthrough.
//
//   - Unclosed scratchpad (only <scratchpad>, no </scratchpad>):
//     returned unchanged. Silent data loss in the worst case is
//     worse than leaking a little preamble; if the model can't
//     even close the tag, fall back to showing everything.
//
//   - Orphan closing tag (only </scratchpad>, no opening):
//     returned unchanged. Symmetric safety case.
//
//   - Multiple scratchpads: only the first block is stripped.
//     The model could (incorrectly) emit a second scratchpad
//     inside the answer; we don't recursively strip.
//
//   - Inline [src:N] citations inside the answer pass through
//     untouched so the downstream InlineSourceLinks post-
//     processor can convert them to clickable links.
//
//   - Leading/trailing whitespace around the block and answer is
//     trimmed so the user doesn't see stray blank lines.
func StripScratchpad(reply string) string {
	start := strings.Index(reply, scratchpadOpen)
	if start == -1 {
		// No opening tag: either no scratchpad at all (clean
		// passthrough) or only a stray closing tag (safety:
		// don't drop anything). Either way, return as-is.
		return reply
	}

	// Find the matching close, but only AFTER the opening tag so
	// a stray </scratchpad> in the preamble doesn't match
	// before the opener.
	closeIdx := strings.Index(reply[start+len(scratchpadOpen):], scratchpadClose)
	if closeIdx == -1 {
		// Unclosed scratchpad: bail out rather than silently
		// dropping everything past the open tag. The model
		// probably forgot the close; better to leak the
		// preamble than to lose the answer.
		return reply
	}

	// closeIdx is relative to (start + len(opener)); convert to
	// absolute.
	closeStart := start + len(scratchpadOpen) + closeIdx
	closeEnd := closeStart + len(scratchpadClose)

	before := reply[:start]
	after := reply[closeEnd:]

	return strings.TrimSpace(before + after)
}
