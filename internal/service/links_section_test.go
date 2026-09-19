package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// linksSectionHeader is the line the LLM is told NOT to emit but
// keeps emitting anyway: a trailing "Links:" section with
// placeholder URLs (e.g. "https://docs.example.com"). The prompt
// asks the model not to do this — inline [src:N] citations cover
// the legitimate cases — but some models keep doing it. The
// post-processor strips the trailing block to keep the answer
// clean.
// TestStripTrailingLinksSection_RemovesTrailingBlock pins the
// headline behavior: a "Links:" line followed by URL lines at the
// end of the reply is stripped.
func TestStripTrailingLinksSection_RemovesTrailingBlock(t *testing.T) {
	in := "I don't have the specific steps for authoring a template.\n\nLinks:\n\nhttps://docs.example.com\n"
	out := StripTrailingLinksSection(in)
	require.Equal(t, "I don't have the specific steps for authoring a template.", out)
}

// TestStripTrailingLinksSection_RemovesBulletListBlock pins the
// variant where the model emits a markdown bullet list under
// "Links:" rather than a bare URL.
func TestStripTrailingLinksSection_RemovesBulletListBlock(t *testing.T) {
	in := "The answer is X.\n\nLinks:\n- https://example.com/a\n- https://example.com/b\n"
	out := StripTrailingLinksSection(in)
	require.Equal(t, "The answer is X.", out)
}

// TestStripTrailingLinksSection_PreservesInlineLinks pins the
// contract that "Links:" mid-reply (not at the end) is NOT
// stripped. The function only targets trailing sections.
func TestStripTrailingLinksSection_PreservesInlineLinks(t *testing.T) {
	in := "First paragraph.\n\nLinks: a placeholder for context.\n\nReal answer."
	out := StripTrailingLinksSection(in)
	require.Equal(t, in, out,
		"a 'Links:' line mid-reply must be preserved")
}

// TestStripTrailingLinksSection_NoTrailingSectionPassesThrough
// pins the no-op case.
func TestStripTrailingLinksSection_NoTrailingSectionPassesThrough(t *testing.T) {
	in := "The answer is X."
	out := StripTrailingLinksSection(in)
	require.Equal(t, in, out)
}

// TestStripTrailingLinksSection_HandlesMarkdownBoldVariant pins
// the variant where the LLM uses markdown bold: "**Links:**"
// or similar. We match the bare "Links:" with optional leading
// markdown formatting because the LLM tends to use bold to make
// its section headers stand out.
func TestStripTrailingLinksSection_HandlesMarkdownBoldVariant(t *testing.T) {
	in := "Answer.\n\n**Links:**\n\nhttps://docs.example.com/\n"
	out := StripTrailingLinksSection(in)
	require.Equal(t, "Answer.", out,
		"markdown-bolded 'Links:' header must still be detected")
}

// TestStripLeadingThinking_HandlesExtendedStarters adds cases
// to the existing test suite for the three new starters added
// in this commit.
func TestStripLeadingThinking_HandlesExtendedStarters(t *testing.T) {
	cases := map[string]string{
		"Looking at the ": "Looking at the entries: ADR-005 etc.\n\nThe answer is X.",
		"Per the ":        "Per the contract, the answer is X.\n\nNo, the answer is Y.",
		"I should answer": "I should answer with what I have.\n\nThe answer is X.",
	}
	for starter, in := range cases {
		t.Run(starter, func(t *testing.T) {
			out := StripLeadingThinking(in)
			require.NotContains(t, out, starter,
				"the '%s' starter must be stripped", starter)
			answerToken := strings.TrimSpace(strings.Split(in, "\n\n")[1])
			require.Contains(t, out, answerToken)
		})
	}
}

// keep import used.
var _ = strings.TrimSpace
