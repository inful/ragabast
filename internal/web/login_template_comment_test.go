package web

// Regression guard for the broken /auth/login layout that
// came from a {{ template "header" .Header }} directive
// sitting inside an HTML comment in templates/login.html.
//
// What happened:
//
// The login template's documentation comment ended with the
// literal text "{{ template "header" .Header }}". Go's
// html/template parser does not always treat the {{ ... }}
// inside <!-- ... --> as inert comment text — when the
// comment appears in the position oauth.go's loginTpl ends
// up executing (i.e. between {{define "login.html"}} and
// the {{ template "header" }} invocation in the body), the
// parser executes the directive at the wrong point in the
// document and produces a duplicate <nav> ABOVE the document
// root <html>. The browser then does tag-soup recovery and
// the page ends up visibly mangled (two navbars, the navbar
// floating at the bottom-center of the page, etc.).
//
// The fix:
//
// Replace the literal {{ template "header" .Header }} in the
// comment with prose that describes the partial without the
// directive syntax. The directive is still authored in the
// body below; the comment is just documentation.
//
// This test scans every embedded template file for
// {{ ... }} blocks that appear inside HTML comments and
// fails if any are found. The contract is conservative: ANY
// template directive inside ANY HTML comment is suspect.
// The few template files that legitimately have braces in
// comments (e.g. chat_message.html showing the HTML structure
// in pseudo-code) intentionally use { ... } syntax (no curly
// braces plus percent signs) so the regex doesn't match
// them. If a future template does need braces in a comment,
// this test will catch the next regression — fix the
// template, don't relax the test.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmbeddedTemplates_NoTemplateDirectiveInComment pins
// the "no {{ ... }} inside HTML comments" contract for every
// embedded template. The regex below matches:
//
//	<!-- followed by any content including a literal {{
//	    (start of an html/template action)
//
// It walks each template, strips out all HTML comments, and
// asserts that nothing matches the inner pattern. A direct
// read + assert is enough — we don't need to render the
// template to detect the bug.
func TestEmbeddedTemplates_NoTemplateDirectiveInComment(t *testing.T) {
	// {{ followed by anything (including whitespace) so we
	// catch any action form ( {{ ... }}, {{- ... -}},
	// {{ if ... }}, {{ .User }}, etc. ). The trailing
	// `}}` anchor is left optional — some directives span
	// multiple lines and the regex is just locating the
	// START of each one for counting purposes.
	const directiveRE = `\{\{[-]?`

	// Walk every embedded template.
	entries, err := templatesFS.ReadDir("templates")
	require.NoError(t, err, "reading embedded templates dir")

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		data, err := templatesFS.ReadFile("templates/" + entry.Name())
		require.NoError(t, err, "reading templates/%s", entry.Name())
		body := string(data)

		// Strip every <!-- ... --> block (including
		// multi-line ones). After this, any directiveRE
		// match must come from real template code, not
		// from a comment.
		stripped := stripHTMLComments(body)

		// The assertion: every template directive in the
		// file must be OUTSIDE an HTML comment. After
		// stripping comments, the count of {{ ... }}
		// occurrences must NOT drop — anything dropped is
		// something that survived the comment strip and
		// was therefore inside a comment.
		total := regexp.MustCompile(directiveRE).FindAllString(body, -1)
		strippedMatches := regexp.MustCompile(directiveRE).FindAllString(stripped, -1)
		assert.Len(t, strippedMatches, len(total),
			"template %s contains a template directive inside an HTML comment — Go's html/template parser does not always treat {{ ... }} inside <!-- ... --> as comment text, which has caused render breakage (see commit message for the {{ template \"header\" .Header }} regression in templates/login.html). Stripped-count = %d, full-count = %d — any difference means a directive was inside a comment.",
			entry.Name(), len(strippedMatches), len(total))
	}
}
