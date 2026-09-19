package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStripScratchpad_RemovesBlock pins the headline behavior:
// when the LLM emits the canonical "<scratchpad>...</scratchpad>"
// format, everything inside the block is dropped and everything
// after is the user-visible answer.
func TestStripScratchpad_RemovesBlock(t *testing.T) {
	in := "<scratchpad>\nThe user is asking about X. Let me check Y.\nLooking at ADR-005.\n</scratchpad>\n\nThe answer is Z."
	out := StripScratchpad(in)
	require.Equal(t, "The answer is Z.", out)
}

// TestStripScratchpad_NoScratchpadLeavesAnswerUntouched pins
// the contract that a reply that doesn't use the scratchpad
// format passes through unchanged. This is the safety case:
// when the model forgets to wrap its preamble, we don't
// strip anything.
func TestStripScratchpad_NoScratchpadLeavesAnswerUntouched(t *testing.T) {
	in := "The answer is Z."
	out := StripScratchpad(in)
	require.Equal(t, "The answer is Z.", out)
}

// TestStripScratchpad_HandlesMultipleBlocks pins the case where
// the model emits two scratchpads (which it shouldn't, but might
// if it's confused). We strip up to and including the FIRST
// closing tag — everything after is the answer. Subsequent
// scratchpads inside the answer are preserved verbatim.
func TestStripScratchpad_HandlesMultipleBlocks(t *testing.T) {
	in := "<scratchpad>\nFirst reasoning\n</scratchpad>\n\nAnswer paragraph one.\n\n<scratchpad>\nWas this useful? No more thinking needed.\n</scratchpad>\n\nAnswer paragraph two."
	out := StripScratchpad(in)
	require.Equal(t,
		"Answer paragraph one.\n\n<scratchpad>\nWas this useful? No more thinking needed.\n</scratchpad>\n\nAnswer paragraph two.",
		out,
		"only the first scratchpad is stripped; subsequent scratchpads inside the answer are preserved",
	)
}

// TestStripScratchpad_UnclosedBlockLeavesReplyUntouched pins
// the safety case where the model forgets to close the scratchpad
// tag. We must NOT silently drop everything past the open tag —
// that would hide a real answer in the worst-case scenario.
// Returning the input unchanged is the safe choice.
func TestStripScratchpad_UnclosedBlockLeavesReplyUntouched(t *testing.T) {
	in := "<scratchpad>\nForgot to close this\n\nThe actual answer is here."
	out := StripScratchpad(in)
	require.Equal(t, in, out,
		"unclosed scratchpad must NOT cause silent data loss")
}

// TestStripScratchpad_OnlyClosingTagLeavesReplyUntouched pins the
// degenerate case where the model emitted a closing tag without
// the opening one. We must NOT silently drop everything before
// the orphan close — same data-loss concern.
func TestStripScratchpad_OnlyClosingTagLeavesReplyUntouched(t *testing.T) {
	in := "Some preamble.\n</scratchpad>\n\nThe actual answer."
	out := StripScratchpad(in)
	require.Equal(t, in, out,
		"orphan closing tag must NOT cause silent data loss")
}

// TestStripScratchpad_HandlesWhitespaceAroundBlocks pins the
// robustness case: the model sometimes wraps the block with
// extra blank lines, leading whitespace, or trailing whitespace.
// The trim inside the function makes sure the answer doesn't
// start with a stray newline.
func TestStripScratchpad_HandlesWhitespaceAroundBlocks(t *testing.T) {
	in := "\n\n<scratchpad>\nreasoning\n</scratchpad>\n\n\nThe answer.\n\n"
	out := StripScratchpad(in)
	require.Equal(t, "The answer.", out,
		"leading and trailing whitespace around the block and answer must be trimmed")
}

// TestStripScratchpad_PreservesInlineCitationsInAnswer pins
// that answer paragraphs containing the [src:N] citation markers
// (used by the InlineSourceLinks post-processor downstream)
// pass through untouched. The scratchpad block is gone; the
// citations live in the answer text and stay there.
func TestStripScratchpad_PreservesInlineCitationsInAnswer(t *testing.T) {
	in := "<scratchpad>\nReasoning.</scratchpad>\n\nSee [src:0] for details."
	out := StripScratchpad(in)
	require.Equal(t, "See [src:0] for details.", out,
		"inline citation markers in the answer must survive")
}

// Reference the strings import so the file builds even when
// the test set is reduced.
var _ = strings.TrimSpace
