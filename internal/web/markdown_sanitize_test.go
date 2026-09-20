package web

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSanitizeForChatHTML_StripsDangerousPayloads pins the
// behavior of the defense-in-depth sanitizer pass that runs
// immediately before the LLM reply is wrapped as template.HTML
// in the chat page (see internal/web/handlers.go
// handleChatMessage and internal/web/markdown.go
// sanitizeForChatHTML).
//
// Every payload here is one a future markdown renderer,
// InlineSourceLinks implementation, or bluemonday bypass could
// accidentally re-introduce. The sanitizer must reject them so
// that a regression in any one layer does not become XSS
// through the chat UI.
func TestSanitizeForChatHTML_StripsDangerousPayloads(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // substrings that MUST NOT appear in the output
	}{
		{
			name: "script tag is stripped",
			in:   `<p>hi</p><script>alert(1)</script>`,
			want: []string{"<script>"},
		},
		{
			name: "img with onerror is stripped",
			in:   `<p>hi</p><img src=x onerror=alert(2)>`,
			want: []string{"onerror="},
		},
		{
			name: "anchor with onclick is stripped",
			in:   `<a href="https://example.com" onclick="alert(3)">x</a>`,
			want: []string{"onclick="},
		},
		{
			name: "javascript: URL is normalized",
			in:   `<a href="javascript:alert(4)">x</a>`,
			want: []string{"javascript:alert(4)"},
		},
		{
			name: "iframe is stripped",
			in:   `<iframe src="https://evil.example"></iframe>`,
			want: []string{"<iframe"},
		},
		{
			name: "object is stripped",
			in:   `<object data="https://evil.example"></object>`,
			want: []string{"<object"},
		},
		{
			name: "svg with onload is stripped",
			in:   `<svg onload="alert(5)"></svg>`,
			want: []string{"onload="},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitizeForChatHTML(tc.in)
			for _, bad := range tc.want {
				require.NotContains(t, out, bad,
					"sanitizeForChatHTML let through %q in %q -> %q", bad, tc.in, out)
			}
		})
	}
}

// TestSanitizeForChatHTML_PreservesLegitimateMarkdown confirms
// the sanitizer does not strip the tags the markdown pipeline
// is supposed to emit. Without this guard, a future change to
// chatSanitizer could turn every chat reply into plain text.
func TestSanitizeForChatHTML_PreservesLegitimateMarkdown(t *testing.T) {
	in := `<p>Hello <strong>world</strong></p><ul><li>one</li><li>two</li></ul>`
	out := sanitizeForChatHTML(in)

	require.Contains(t, out, "<p>")
	require.Contains(t, out, "<strong>")
	require.Contains(t, out, "<ul>")
	require.Contains(t, out, "<li>one</li>")
}
