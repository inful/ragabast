package web

// Tests for the login page's flex layout. The user reported
// that with more than one auth provider the menubar floated
// in the middle of the page next to the login form (a flex
// row with align-items: center laid them out side-by-side).
// The fix made the body a flex *column* so the navbar sits
// at the top and the <main> login form is centered in the
// area below it. These tests pin that layout so a future
// contributor who re-introduces a flex row gets a red test.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoginCSS_BodyIsFlexColumn pins the "login page body is
// a flex column so the navbar sits at the top and the <main>
// sits below it" contract. The pre-fix design was a flex row
// with `align-items: center; justify-content: center` which
// laid the navbar and the login form out side-by-side and
// vertically centered them in the viewport — the navbar
// floated in the middle of the page. The fix is
// `display: flex; flex-direction: column;` so the page
// stacks the navbar above the main.
//
// We test the CSS rule directly (not the rendered HTML) so
// a future contributor who changes the layout to e.g. grid
// or removes the body block entirely still gets a red test
// pointing at the contract.
func TestLoginCSS_BodyIsFlexColumn(t *testing.T) {
	css := readStaticAsset(t, "/static/login.css")
	require.NotEmpty(t, css, "login.css must be readable from the embedded bundle")

	require.Contains(t, css, "body {",
		"login.css must still target body — it's the page-chrome file for the login route")

	// The body block must declare `display: flex` and
	// `flex-direction: column`. We accept the rules in
	// either order (a future contributor might write
	// `flex-direction: column; display: flex;`) and
	// accept any whitespace separator.
	bodyBlock := extractBlock(t, css, "body")
	assert.Regexp(t, `display\s*:\s*flex`, bodyBlock,
		"login.css body must declare display: flex so the navbar (first child) and <main> (second child) are flex items")
	assert.Regexp(t, `flex-direction\s*:\s*column`, bodyBlock,
		"login.css body must declare flex-direction: column so the navbar stacks ABOVE the <main>, not next to it (a flex row would re-introduce the side-by-side menubar + form bug)")
}

// TestLoginCSS_MainFillsRemainingHeight pins the "main
// grows to fill the area below the navbar" contract. With
// `flex: 1` the <main> takes all remaining vertical space,
// and its own flex column layout then centers the login
// card within that area. Without `flex: 1` the <main>
// would shrink to its content height and the login form
// would stick to the top of the page (the navbar-to-form
// gap would be zero).
func TestLoginCSS_MainFillsRemainingHeight(t *testing.T) {
	css := readStaticAsset(t, "/static/login.css")
	require.NotEmpty(t, css, "login.css must be readable from the embedded bundle")

	mainBlock := extractBlock(t, css, "main")
	assert.Regexp(t, `flex\s*:\s*1`, mainBlock,
		"login.css main must declare flex: 1 so it fills the height below the navbar — without it, the login form sticks to the top of the page")
	assert.Regexp(t, `display\s*:\s*flex`, mainBlock,
		"login.css main must declare display: flex so the login card can be centered within it")
	assert.Regexp(t, `flex-direction\s*:\s*column`, mainBlock,
		"login.css main must declare flex-direction: column so the h1, provider buttons, and help links stack vertically (a flex row would lay them out side-by-side — the pre-fix mistake)")
}

// TestLoginCSS_MainIsCenteredHorizontally pins the
// "login card is centered in the viewport, not flush
// against the left edge" contract. The body is a flex
// column with the default `align-items: stretch` — without
// an explicit centering rule on the <main>, the flex item
// would stretch to the body width (capped at max-w-md,
// but flush against the left). The fix is `align-self:
// center` on the <main>.
//
// This is the final piece of the layout fix. The earlier
// pieces (body flex column, main flex 1) put the card in
// the right vertical position; this one puts it in the
// right horizontal position.
func TestLoginCSS_MainIsCenteredHorizontally(t *testing.T) {
	css := readStaticAsset(t, "/static/login.css")
	require.NotEmpty(t, css, "login.css must be readable from the embedded bundle")

	mainBlock := extractBlock(t, css, "main")
	assert.Contains(t, mainBlock, "align-self: center",
		"login.css main must declare align-self: center so the login card is centered in the viewport (without it, the flex item stretches to the body width via the column's default align-items: stretch, and the card sits flush against the left edge)")
}

// extractBlock pulls the body of the first CSS rule whose
// selector matches `name`. Returns the text between the
// opening `{` and matching `}`. If the selector doesn't
// appear, the test fails (so the failure message points at
// the missing rule rather than a confusing "no match").
func extractBlock(t *testing.T, css, name string) string {
	t.Helper()
	idx := strings.Index(css, name+" {")
	if idx < 0 {
		idx = strings.Index(css, name+"{")
	}
	if idx < 0 {
		t.Fatalf("css does not contain a %q rule", name)
	}
	open := strings.Index(css[idx:], "{")
	if open < 0 {
		t.Fatalf("css contains %q but no opening brace", name)
	}
	open += idx
	depth := 0
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[open+1 : i]
			}
		}
	}
	t.Fatalf("css block for %q has no matching closing brace", name)
	return ""
}
