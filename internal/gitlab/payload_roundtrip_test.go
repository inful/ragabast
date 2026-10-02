// Package gitlab_test holds the integration tests that go
// outside the gitlab package's own boundary — specifically the
// round-trip through the real docbuilder parser. Lives in its
// own _test package so the gitlab source package stays
// parser-free (the parser dependency would otherwise leak into
// every caller of internal/gitlab).
package gitlab_test

import (
	"strings"
	"testing"

	"github.com/ragabast/internal/gitlab"
	"github.com/ragabast/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRoundTrip_ThroughDocbuilderParser pins the contract that
// matters most: the markdown produced by EnvelopeToDocbuilderMarkdown
// flows cleanly through the docbuilder parser, with the expected
// UID, Tags, URLs, Title, and Categories landing on the parsed
// *models.Document. Without this test, a future change in either
// side (payload.go or parser.go) would silently break the GitLab
// ingest path until operators started seeing 400 errors.
//
// Both the parser and the conversion are real — no mocks. That's
// the integration point we care about.
func TestRoundTrip_ThroughDocbuilderParser(t *testing.T) {
	env := gitlab.IssueEnvelope{
		Issue: gitlab.IssuePayload{
			IID:         42,
			ProjectID:   4,
			Title:       "Auth: SAML timeout",
			Description: "## Auth\n\nMarkdown body\n",
			State:       "opened",
			Labels:      []string{"bug", "backend"},
			WebURL:      "https://gitlab.example.com/group/bar/-/issues/42",
			CreatedAt:   "2026-01-15T10:00:00.000Z",
			UpdatedAt:   "2026-01-15T13:42:00.000Z",
			IssueType:   "issue",
		},
		PathWithNamespace: "group/bar",
	}

	md, _, err := gitlab.EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)

	// Round-trip through the real parser.
	p := parser.NewDocbuilderParser()
	doc, err := p.ParseDocument([]byte(md), "gitlab-issue")
	require.NoError(t, err,
		"the produced markdown must round-trip cleanly through the docbuilder parser")

	// Headline assertions: the GitLab-issue shape maps onto the
	// standard *models.Document fields.
	assert.Equal(t, "gitlab:group/bar:42", doc.UID,
		"the UID must encode path_with_namespace and iid so it's globally unique across GitLab sources")
	assert.Equal(t, "Auth: SAML timeout", doc.Title,
		"the title round-trips verbatim")
	assert.Equal(t,
		[]string{"https://gitlab.example.com/group/bar/-/issues/42"},
		doc.URLs,
		"the web_url must land in URLs so chat citations link back to GitLab")
	assert.Contains(t, doc.Tags, "gitlab-issue",
		"every GitLab issue must carry the gitlab-issue tag")
	assert.Contains(t, doc.Tags, "gitlab:group/bar",
		"every GitLab issue must carry the path-scoped tag")
	assert.Contains(t, doc.Tags, "bug")
	assert.Contains(t, doc.Tags, "backend")
	assert.Equal(t, []string{"issue"}, doc.Categories,
		"the issue_type populates the categories list so the chat surface can group by kind")
	assert.NotEmpty(t, doc.Fingerprint,
		"the parser computes a fingerprint when frontmatter omits it")
	assert.Equal(t, doc.UID, doc.ID,
		"the parser pins doc.ID = doc.UID for stable dedupe")

	// Body: trim the leading `# <title>` since the parser
	// trims body whitespace; the description content should
	// still be present.
	assert.Contains(t, doc.Content, "## Auth")
	assert.Contains(t, doc.Content, "Markdown body")
}

// TestRoundTrip_NotesThroughParser checks the notes-survive
// the parser path: a GitLab issue with two comments produces
// a parsed doc whose body includes both comments in order.
func TestRoundTrip_NotesThroughParser(t *testing.T) {
	env := gitlab.IssueEnvelope{
		Issue: gitlab.IssuePayload{
			IID: 42, Title: "t",
			CreatedAt: "2026-01-15T10:00:00.000Z",
			WebURL:    "https://gitlab.example.com/group/bar/-/issues/42",
		},
		PathWithNamespace:  "group/bar",
		IncludeNotes:       true,
		IncludeSystemNotes: false,
		Notes: []gitlab.NotePayload{
			{
				ID: 305, Body: "Bob's later comment.",
				CreatedAt: "2026-01-15T13:42:00.000Z",
				Author:    gitlab.Author{Username: "bob"},
			},
			{
				ID: 304, Body: "Alice's earlier comment.",
				CreatedAt: "2026-01-15T11:00:00.000Z",
				Author:    gitlab.Author{Username: "alice"},
			},
		},
	}

	md, sysFiltered, err := gitlab.EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	require.Equal(t, 0, sysFiltered, "no system notes in this fixture")

	p := parser.NewDocbuilderParser()
	doc, err := p.ParseDocument([]byte(md), "gitlab-issue")
	require.NoError(t, err)

	aliceIdx := strings.Index(doc.Content, "Alice's earlier comment.")
	bobIdx := strings.Index(doc.Content, "Bob's later comment.")
	require.NotEqual(t, -1, aliceIdx, "alice's note must be in the parsed content")
	require.NotEqual(t, -1, bobIdx, "bob's note must be in the parsed content")
	assert.Less(t, aliceIdx, bobIdx,
		"the parser doesn't reorder — the chronological order the converter emits is what gets parsed")

	assert.Contains(t, doc.Content, "## Comments",
		"the comments section heading must survive the parser so the chunker splits on it")
}
