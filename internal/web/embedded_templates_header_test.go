package web

import (
	"html/template"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSearchPage_AlwaysOnNavbar pins the fix for the follow-on
// to issue #85: every embedded page template that renders a full
// page must carry the navbar so the always-on nav (Chat |
// Search | Documents | Ingest) renders regardless of which
// template path the server took. Before this fix, only the
// fallback renderer's Go-string templates had the navbar; the
// embedded templates shipped in templates/ did not include
// {{ template "header" .Header }}, so a default install (which
// uses the embedded set) showed no nav on /search or the OAuth
// login chooser.
//
// The chat_message and search_results fragments are excluded:
// they are swapped into specific containers via htmx (chat-messages
// and search-results respectively), so including the navbar in
// them would render the nav inside the swap target — visually
// wrong. The full /search page already has the navbar and the
// initial /chat GET uses the fallback (chat.html is not in the
// embedded set), so the navbar is on the page either way.
func TestSearchPage_AlwaysOnNavbar(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/search.html")
	require.NoError(t, err, "reading templates/search.html")
	body := string(data)
	require.Contains(t, body, `{{ template "header" .Header }}`,
		"templates/search.html must include the header partial so the navbar renders under the embedded template path")
}

// TestLoginPage_AlwaysOnNavbar pins the same for the OAuth
// chooser page (templates/login.html).
func TestLoginPage_AlwaysOnNavbar(t *testing.T) {
	data, err := templatesFS.ReadFile("templates/login.html")
	require.NoError(t, err, "reading templates/login.html")
	body := string(data)
	require.Contains(t, body, `{{ template "header" .Header }}`,
		"templates/login.html must include the header partial so the navbar renders under the embedded template path")
}

// TestPageHeaderBlock_Defined pins that the parsed embedded
// templates expose a "header" template block. The block is the
// unit {{ template "header" .Header }} invokes in every page
// body; if the parsed set doesn't register it the per-page
// calls would no-op and the navbar would silently disappear.
// NewServer parses the block into the same *template.Template
// that holds the page bodies — this test re-runs the same
// ParseFS + Parse(pageHeaderFallbackBody) sequence to assert
// the lookup works end-to-end.
func TestPageHeaderBlock_Defined(t *testing.T) {
	tmpl, err := template.New("base").ParseFS(templatesFS, "templates/*.html")
	require.NoError(t, err, "parsing embedded templates")
	// Register the header block the same way NewServer does.
	_, err = tmpl.Parse(pageHeaderFallbackBody)
	require.NoError(t, err, "parsing header block")
	require.NotNil(t, tmpl.Lookup("header"),
		"the embedded template set must register a `header` block so {{ template \"header\" .Header }} resolves at render time")
}
