package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppendLinksSection_AppendsWhenMissing(t *testing.T) {
	out := appendLinksSection("Answer text.", []string{"https://example.com/a", "https://example.com/b"})
	require.Contains(t, out, "Answer text.")
	require.Contains(t, out, "\n\nLinks:\n")
	require.Contains(t, out, "- https://example.com/a")
	require.Contains(t, out, "- https://example.com/b")
}

func TestAppendLinksSection_DoesNotDuplicate(t *testing.T) {
	in := "Answer text.\n\nLinks:\n- https://example.com/a"
	out := appendLinksSection(in, []string{"https://example.com/a", "https://example.com/b"})
	require.Equal(t, in, out)
}
