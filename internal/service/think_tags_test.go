package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStripThinkTags_RemovesBlock pins the headline behavior:
// <think>...</think> blocks (the model's own reasoning tokens)
// are removed before the reply reaches the user. Without this,
// the user sees the model's chain-of-thought reasoning mixed
// in with the actual answer.
func TestStripThinkTags_RemovesBlock(t *testing.T) {
	in := "<think>Let me look through the context.</think>\n\nThe answer is X."
	out := StripThinkTags(in)
	require.Equal(t, "The answer is X.", out)
}

// TestStripThinkTags_NoThinkTagsLeavesReplyUntouched pins the
// safety case: a reply without <think> tags passes through
// unchanged.
func TestStripThinkTags_NoThinkTagsLeavesReplyUntouched(t *testing.T) {
	in := "The answer is X."
	out := StripThinkTags(in)
	require.Equal(t, "The answer is X.", out)
}

// TestStripThinkTags_HandlesMultipleBlocks pins the case where
// the model emits two <think> blocks (which it shouldn't, but
// might if it gets confused). Only the first block is stripped;
// the second is preserved if it's mid-reply.
func TestStripThinkTags_HandlesMultipleBlocks(t *testing.T) {
	in := "<think>first reasoning</think>\n\nAnswer one.\n\n<think>second reasoning</think>\n\nAnswer two."
	out := StripThinkTags(in)
	require.Equal(
		t,
		"Answer one.\n\n<think>second reasoning</think>\n\nAnswer two.",
		out,
	)
}

// TestStripThinkTags_UnclosedBlockLeavesReplyUntouched pins the
// safety case: an unclosed <think> tag must not cause silent
// data loss. Return unchanged.
func TestStripThinkTags_UnclosedBlockLeavesReplyUntouched(t *testing.T) {
	in := "<think>Forgot to close\n\nThe actual answer is here."
	out := StripThinkTags(in)
	require.Equal(t, in, out,
		"unclosed think tag must NOT cause silent data loss")
}

// TestStripThinkTags_HandlesWhitespaceAroundBlocks pins the
// robustness case: the model often wraps the block with extra
// blank lines, leading whitespace, or trailing whitespace.
func TestStripThinkTags_HandlesWhitespaceAroundBlocks(t *testing.T) {
	in := "\n\n<think>reasoning</think>\n\nThe answer.\n\n"
	out := StripThinkTags(in)
	require.Equal(t, "The answer.", out,
		"leading and trailing whitespace around the block and answer must be trimmed")
}

// TestStripThinkTags_HandlesSelfClosingForm pins the variant
// where the model emits a self-closing <think>...</think> (no
// separate closing tag — some Qwen-style tokenizers emit this).
func TestStripThinkTags_HandlesSelfClosingForm(t *testing.T) {
	in := "<think>reasoning</think>The answer is X."
	out := StripThinkTags(in)
	require.Equal(t, "The answer is X.", out)
}

// TestStripThinkTags_HandlesAngleBracketVariant pins the case
// where the model uses angle-bracket-free Qwen-style thinking.
// Some models emit `[think]...[/think]` instead of the HTML-ish
// form. The trailing space and bracket syntax variations are
// common.
func TestStripThinkTags_HandlesBracketVariant(t *testing.T) {
	in := "[think]reasoning[/think]\n\nThe answer."
	out := StripThinkTags(in)
	require.Equal(t, "The answer.", out,
		"[think]...[/think] variant must also be stripped")
}

// TestStripThinkTags_HandlesCoexistWithScratchpad pins the case
// where both formats appear in the same reply. StripThinkTags
// runs first and removes only the <think> block; StripScratchpad
// is responsible for the <scratchpad> block on its own pass.
// This test pins the contract: after StripThinkTags alone, the
// scratchpad is preserved so the next post-processor can handle it.
func TestStripThinkTags_HandlesCoexistWithScratchpad(t *testing.T) {
	in := "<think>model reasoning</think>\n\n<scratchpad>user scratchpad</scratchpad>\n\nThe answer is X."
	out := StripThinkTags(in)
	require.Equal(t, "<scratchpad>user scratchpad</scratchpad>\n\nThe answer is X.", out,
		"StripThinkTags must remove the <think> block but leave the <scratchpad> block for StripScratchpad to handle")
}
