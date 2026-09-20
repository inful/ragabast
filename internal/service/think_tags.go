package service

// StripThinkTags removes the model's own reasoning tokens from a
// reply — the <think>...</think> blocks that some model families
// (notably Qwen and DeepSeek) emit as a parallel reasoning
// channel. Without this stripper, the user's visible chat reply
// includes the model's "thinking out loud" preamble.
//
// This is a defense for models that have their own native thinking
// token format AND for models that ignore ragabast's
// `<scratchpad>...</scratchpad>` directive and instead emit
// `<think>...</think>`. The chat handler strips both formats in
// series:
//
//  1. StripThinkTags (this function) — runs first, removes the
//     native thinking block.
//  2. StripScratchpad — runs second, removes the ragabast-
//     specific preamble block.
//  3. StripLeadingThinking — runs last, heuristic catch-all
//     for paragraphs that don't use either marker.
//
// Safety contracts (verified by tests):
//
//   - Reply with no <think> tags: returned unchanged.
//   - Unclosed <think> tag (no </think>): returned unchanged.
//     Silent data loss is the worst possible failure mode;
//     "show everything" is always safer.
//   - Self-closing form <think>...</think> is handled.
//   - Bracket-form [think]...[/think] (Qwen variant without
//     angle brackets) is also handled — useful because some
//     tokenizers alternate between the two depending on context.
//   - Multiple <think> blocks: only the first is stripped.
//   - Coexistence with <scratchpad>: the think block is stripped
//     here; the scratchpad block is preserved so StripScratchpad
//     can strip it on its own pass.
//
// Why both <think> AND [think]: the model's tokenizer
// sometimes emits the angle-bracket form, sometimes the plain
// bracket form depending on context. We catch both rather than
// pick one and miss the other.
func StripThinkTags(reply string) string {
	return stripDelimitedBlock(
		reply,
		[]string{"<think>", "[think]"},
		[]string{"</think>", "[/think]"},
	)
}
