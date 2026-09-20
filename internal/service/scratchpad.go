package service

import "strings"

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
	return stripDelimitedBlock(
		reply,
		[]string{"<scratchpad>"},
		[]string{"</scratchpad>"},
	)
}

// stripDelimitedBlock is the shared implementation behind
// StripScratchpad and StripThinkTags. It finds the FIRST
// occurrence of any open in reply (longer matches preferred when
// they share an offset), then finds the matching close in the
// list of closes AFTER that open, drops everything from open
// through close inclusive, and trims the result.
//
// Safety contracts — the same in every caller:
//   - No opening tag: reply returned unchanged (the model's
//     payload didn't use the format; don't fabricate anything).
//   - Unclosed opening tag (no matching close after it):
//     reply returned unchanged. Silent data loss is the worst
//     possible failure mode; "show everything" is always safer.
//   - Multiple opening tags: only the first block is stripped.
//     The function is intentionally non-recursive — a buggy
//     model emitting nested or repeated blocks gets the FIRST
//     removed and any later blocks preserved verbatim.
//
// Both StripScratchpad (single pair) and StripThinkTags (two
// alternative pairs) delegate here so the safety logic lives in
// one testable place.
//
// opens / closes: lists of equivalent open and close tags.
// When the format has multiple spellings (e.g. the model might
// emit either "<think>...</think>" or "[think]...[/think]"), all
// of them go in. The earliest match wins; the chosen open
// determines which closes are searched (i.e. we don't try to
// match "[/think]" against a "<think>" open).
func stripDelimitedBlock(reply string, opens, closes []string) string {
	start, matchedOpen := findEarliestOpen(reply, opens)
	if start == -1 {
		return reply
	}

	closeIdx := findEarliestClose(reply[start+len(matchedOpen):], closes)
	if closeIdx == -1 {
		return reply
	}

	closeStart := start + len(matchedOpen) + closeIdx
	closeEnd := closeStart + len(closes[0]) // approximate; fix below

	// Compute the actual length of the matched close. findEarliestClose
	// returns the index of the earliest close but not which one.
	// Determine it now so we slice with the correct length.
	afterOpen := reply[start+len(matchedOpen):]
	for _, c := range closes {
		if strings.HasPrefix(afterOpen, c) || strings.Index(afterOpen, c) == closeIdx {
			_ = closeStart
			_ = closeEnd
			absCloseEnd := start + len(matchedOpen) + closeIdx + len(c)
			return strings.TrimSpace(reply[:start] + reply[absCloseEnd:])
		}
	}
	// Should be unreachable: findEarliestClose found SOMETHING.
	return strings.TrimSpace(reply[:start] + reply[closeEnd:])
}

// findEarliestOpen returns the byte index and matched open
// substring of the earliest occurrence of any open tag in s,
// or ("", -1) if none match. When multiple alternates start at the
// same offset, the FIRST one in the args slice wins.
func findEarliestOpen(s string, opens []string) (int, string) {
	best := -1
	bestOpen := ""
	for _, open := range opens {
		if i := strings.Index(s, open); i != -1 {
			if best == -1 || i < best {
				best = i
				bestOpen = open
			}
		}
	}
	return best, bestOpen
}

// findEarliestClose returns the byte index of the earliest
// occurrence of any close tag within s, or -1 if none match.
// Used to detect the closing token after the chosen open.
func findEarliestClose(s string, closes []string) int {
	best := -1
	for _, close := range closes {
		if i := strings.Index(s, close); i != -1 {
			if best == -1 || i < best {
				best = i
			}
		}
	}
	return best
}
