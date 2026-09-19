package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStripLeadingThinking_RemovesLetMePreamble pins the
// failure mode the user just hit: the model emitted a long
// chain of "Let me look at X / Actually / Wait" self-narration
// before the actual answer. The post-processor must strip
// that preamble and keep only the final answer.
func TestStripLeadingThinking_RemovesLetMePreamble(t *testing.T) {
	in := `Let me look through the context to find what docbuilder does.

Looking at the context:
- ADR-005 is about linting
- The Configuration Reference describes settings

I don't know. The context describes capabilities but no blind-spots list.

That is the final answer.`
	out := StripLeadingThinking(in)
	require.Equal(
		t,
		"I don't know. The context describes capabilities but no blind-spots list.\n\nThat is the final answer.",
		out,
	)
}

// TestStripLeadingThinking_HandlesMultipleThinkStarters pins
// the same fix for the other common thinking starters a model
// emits: "Wait,", "Actually,", "Hmm,", "I need to", "First,".
func TestStripLeadingThinking_HandlesMultipleThinkStarters(t *testing.T) {
	cases := map[string]string{
		"Wait,":          "Wait, let me reconsider.\n\nThe answer is X.",
		"Actually,":      "Actually, I think the answer is Y.\n\nNo, scratch that. Z.",
		"Hmm,":           "Hmm, this is tricky.\n\nBut the answer is clear: W.",
		"I need to ":     "I need to check the math.\n\nSo 42.",
		"First, let me ": "First, let me list the docs.\n\nThen the answer.",
		"Looking at ":    "Looking at ADR-005, it says...\n\nSo the answer.",
	}
	for starter, in := range cases {
		t.Run(starter, func(t *testing.T) {
			out := StripLeadingThinking(in)
			require.NotContains(t, out, starter,
				"the '%s' starter must not appear in the stripped output", starter)
			// The real answer text must survive intact. Each case
			// uses a distinctive trailing token ("X", "Y", "Z", etc.)
			// so we can assert exactly that the answer made it
			// through, not just that the starter was removed.
			answerToken := strings.TrimSpace(strings.Split(in, "\n\n")[1])
			require.Contains(t, out, answerToken,
				"the real answer (%q) must survive stripping", answerToken)
		})
	}
}

// TestStripLeadingThinking_PreservesRealAnswerAtStart pins the
// contract that a reply whose first paragraph IS the answer
// must pass through untouched.
func TestStripLeadingThinking_PreservesRealAnswerAtStart(t *testing.T) {
	in := `The answer is straightforward: ragabast uses chromem-go for storage.

See ADR-005 for details.`
	out := StripLeadingThinking(in)
	require.Equal(t, in, out,
		"a reply that starts with a real answer must not be touched")
}

// TestStripLeadingThinking_LeavesMiddleThinkingAlone pins the
// contract that thinking is only stripped at the start. A reply
// where the LLM starts cleanly and only later thinks out loud
// is left alone — the user might actually want that self-
// correction visible.
func TestStripLeadingThinking_LeavesMiddleThinkingAlone(t *testing.T) {
	in := `The answer is X.

Wait, let me reconsider this.

Actually, no, X is correct.`
	out := StripLeadingThinking(in)
	require.Equal(t, in, out,
		"thinking after a real opening sentence must not be stripped")
}

// TestStripLeadingThinking_StripsBlankLineSeparatedThinking pins
// the contract that multiple thinking paragraphs separated by
// blank lines are all stripped.
func TestStripLeadingThinking_StripsBlankLineSeparatedThinking(t *testing.T) {
	in := `Let me check ADR-001.

Looking at the doc.

Actually, ADR-001 doesn't say anything about this.

The answer is X.`
	out := StripLeadingThinking(in)
	require.Equal(t, "The answer is X.", out,
		"multiple thinking paragraphs separated by blank lines must all be stripped")
}

// TestStripLeadingThinking_OnlyThinkersAtStart pins the
// boundary: thinking paragraphs at the start are stripped,
// thinking paragraphs after the first real one are kept.
func TestStripLeadingThinking_OnlyThinkersAtStart(t *testing.T) {
	in := `Let me start.

The real answer starts here.

Wait, but here's a mid-stream correction.`
	out := StripLeadingThinking(in)
	require.NotContains(t, out, "Let me start",
		"the leading thinking must be stripped")
	require.Contains(t, out, "The real answer starts here")
	require.Contains(t, out, "Wait, but here's a mid-stream correction",
		"mid-stream thinking after the real answer must be preserved")
}

// TestStripLeadingThinking_HandlesLeadingWhitespace pins the
// contract that a leading blank line is tolerated without
// false-stripping the first real answer.
func TestStripLeadingThinking_HandlesLeadingWhitespace(t *testing.T) {
	in := "\n\nThe answer is X."
	out := StripLeadingThinking(in)
	require.Equal(t, "The answer is X.", out,
		"leading blank lines must not be treated as a thinking block")
}
