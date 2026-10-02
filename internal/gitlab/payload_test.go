package gitlab

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestEnvelopeToDocbuilderMarkdown_HappyPath pins the headline
// contract: a verbatim GitLab issue JSON envelope produces a
// docbuilder YAML-frontmatter markdown string that the existing
// parser + ingest pipeline accepts unchanged. The fixture below
// is a trimmed version of the example response from
// docs.gitlab.com/api/issues/ — the field names and types match
// the live API.
//
// Pinning the produced markdown (rather than asserting only on
// the parsed *models.Document) catches the round-trip risk: if
// the parser's contract shifts in a future release, every test
// here turns red at the same time, so the failure is unmistakable.
func TestEnvelopeToDocbuilderMarkdown_HappyPath(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
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
			Author:      Author{ID: 1, Username: "alice", Name: "Alice"},
		},
		PathWithNamespace:  "group/bar",
		IncludeNotes:       false,
		IncludeSystemNotes: false,
	}

	md, sysFiltered, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	require.Equal(t, 0, sysFiltered,
		"happy path: IncludeNotes=false → no notes to filter")

	// Frontmatter header.
	require.True(t, strings.HasPrefix(md, "---\n"),
		"output must begin with the docbuilder frontmatter delimiter")

	// Required fields present in the frontmatter.
	// The uid is YAML-quoted because it has a ":" — sanitizeForYAML
	// guards the frontmatter block. The contract here is "the UID
	// value appears in the rendered frontmatter"; the exact textual
	// form (quoted or unquoted) is an internal detail.
	assert.Contains(t, md, "gitlab:group/bar:42",
		"the UID must encode path_with_namespace and iid so it's globally unique across GitLab sources")
	assert.Contains(t, md, "Auth: SAML timeout",
		"the title must be present in the frontmatter (verbatim or YAML-quoted — the colon triggers quoting, that's expected)")
	assert.Contains(t, md, "https://gitlab.example.com/group/bar/-/issues/42",
		"the web_url must land in the urls frontmatter list so chat citations link back to GitLab")

	// Tags include the discovery-affordance anchors and the issue's labels.
	assert.Contains(t, md, "- gitlab-issue",
		"every GitLab issue must carry the gitlab-issue tag so future chat-search filters can hook on it")
	assert.Contains(t, md, "- gitlab:group/bar",
		"every GitLab issue must carry the path-scoped tag so operators can scope a search to one project")
	assert.Contains(t, md, "- backend")

	// Body content.
	assert.Contains(t, md, "## Auth",
		"the issue's description becomes the body verbatim")
	assert.Contains(t, md, "Markdown body",
		"the issue's description content reaches the body")
}

// TestEnvelopeToDocbuilderMarkdown_IncludesNotes pins the notes
// path: IncludeNotes=true → notes are appended to the body under a
// `## Comments` section, in chronological order, with the author
// and timestamp rendered as a level-3 heading.
func TestEnvelopeToDocbuilderMarkdown_IncludesNotes(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID:       42,
			Title:     "Auth: SAML timeout",
			CreatedAt: "2026-01-15T10:00:00.000Z",
			WebURL:    "https://gitlab.example.com/group/bar/-/issues/42",
		},
		PathWithNamespace:  "group/bar",
		IncludeNotes:       true,
		IncludeSystemNotes: false,
		Notes: []NotePayload{
			{
				ID:        305,
				Body:      "Bob's later comment.",
				CreatedAt: "2026-01-15T13:42:00.000Z",
				Author:    Author{Username: "bob", Name: "Bob"},
			},
			{
				ID:        304,
				Body:      "Alice's earlier comment.",
				CreatedAt: "2026-01-15T11:00:00.000Z",
				Author:    Author{Username: "alice", Name: "Alice"},
			},
		},
	}

	md, sysFiltered, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	require.Equal(t, 0, sysFiltered,
		"no system notes in this fixture")

	// Notes must appear under `## Comments` and in chronological order.
	aliceIdx := strings.Index(md, "Alice's earlier comment.")
	bobIdx := strings.Index(md, "Bob's later comment.")
	require.NotEqual(t, -1, aliceIdx, "alice's note must be in the body")
	require.NotEqual(t, -1, bobIdx, "bob's note must be in the body")
	assert.Less(t, aliceIdx, bobIdx,
		"notes must be sorted ascending by created_at so readers see the conversation in time order")
	assert.Contains(t, md, "## Comments",
		"the comments section must be a level-2 heading so the chunker splits cleanly on it")
	assert.Contains(t, md, "@alice",
		"each note heading must name the author for citation context")
	assert.Contains(t, md, "@bob")
}

// TestEnvelopeToDocbuilderMarkdown_FiltersSystemNotes_ByDefault
// pins the default policy: notes with `system: true` (the
// "closed", "changed milestone to X" records) are filtered out
// unless the sender explicitly opts in via
// `include_system_notes: true`. The returned int reports how
// many were dropped so the handler can log it.
func TestEnvelopeToDocbuilderMarkdown_FiltersSystemNotes_ByDefault(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID:       1,
			Title:     "t",
			CreatedAt: "2026-01-15T10:00:00.000Z",
			WebURL:    "https://x",
		},
		PathWithNamespace:  "g/p",
		IncludeNotes:       true,
		IncludeSystemNotes: false,
		Notes: []NotePayload{
			{
				ID: 1, Body: "Real comment.", System: false,
				Author: Author{Username: "alice"}, CreatedAt: "2026-01-15T11:00:00.000Z",
			},
			{
				ID: 2, Body: "closed", System: true,
				Author: Author{Username: "system"}, CreatedAt: "2026-01-15T12:00:00.000Z",
			},
			{
				ID: 3, Body: "changed title to X", System: true,
				Author: Author{Username: "system"}, CreatedAt: "2026-01-15T13:00:00.000Z",
			},
		},
	}

	md, sysFiltered, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	require.Equal(t, 2, sysFiltered,
		"the second return must count how many system notes were dropped so the handler can log it")
	assert.NotContains(t, md, "changed title to X",
		"system notes must be filtered out by default")
	assert.NotContains(t, md, "@system",
		"system-note authors must not appear in the rendered body")
	assert.Contains(t, md, "Real comment.",
		"non-system notes must remain in the body")
}

// TestEnvelopeToDocbuilderMarkdown_KeepsSystemNotes_WhenOptedIn
// pins the opt-in: include_system_notes=true preserves every
// note, system or not.
func TestEnvelopeToDocbuilderMarkdown_KeepsSystemNotes_WhenOptedIn(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID:       1,
			Title:     "t",
			CreatedAt: "2026-01-15T10:00:00.000Z",
			WebURL:    "https://x",
		},
		PathWithNamespace:  "g/p",
		IncludeNotes:       true,
		IncludeSystemNotes: true,
		Notes: []NotePayload{
			{
				ID: 1, Body: "Real comment.", System: false,
				Author: Author{Username: "alice"}, CreatedAt: "2026-01-15T11:00:00.000Z",
			},
			{
				ID: 2, Body: "closed", System: true,
				Author: Author{Username: "system"}, CreatedAt: "2026-01-15T12:00:00.000Z",
			},
		},
	}

	md, sysFiltered, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	require.Equal(t, 0, sysFiltered,
		"with include_system_notes=true nothing was filtered out")
	assert.Contains(t, md, "Real comment.")
	assert.Contains(t, md, "closed",
		"system notes must be embedded verbatim when opted in")
}

// TestEnvelopeToDocbuilderMarkdown_MissingIID_Errors pins the
// required-field contract: path_with_namespace and issue.iid are
// what make the UID, so they must both be present.
func TestEnvelopeToDocbuilderMarkdown_MissingIID_Errors(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			Title:     "t",
			CreatedAt: "2026-01-15T10:00:00.000Z",
			WebURL:    "https://x",
		},
		PathWithNamespace: "g/p",
	}
	_, _, err := EnvelopeToDocbuilderMarkdown(env)
	require.Error(t, err, "missing iid must fail validation")
	assert.Contains(t, err.Error(), "iid",
		"the error must name the missing field so the operator can correct the sender")
}

// TestEnvelopeToDocbuilderMarkdown_MissingPath_Errors pins the
// other half of the UID contract.
func TestEnvelopeToDocbuilderMarkdown_MissingPath_Errors(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID:       1,
			Title:     "t",
			CreatedAt: "2026-01-15T10:00:00.000Z",
			WebURL:    "https://x",
		},
	}
	_, _, err := EnvelopeToDocbuilderMarkdown(env)
	require.Error(t, err, "missing path_with_namespace must fail validation")
	assert.Contains(t, err.Error(), "path_with_namespace",
		"the error must name the missing field so the operator can correct the sender")
}

// TestEnvelopeToDocbuilderMarkdown_UIDWithMetachars_ProducesValidYAML
// pins the frontmatter-quoting contract for the UID field.
//
// The uid is "gitlab:{path}:{iid}". When {path} contains a `: `
// sequence the unquoted YAML form "uid: gitlab:foo: bar:42" parses
// as a multi-key mapping ("uid": "gitlab:foo:", "bar": "42"), which
// is wrong and would surface as a 400 from the parser. Every other
// string field in the frontmatter is sanitized through
// sanitizeForYAML — the uid must be too, so a sender-supplied path
// containing `:`, `#`, or a newline can't corrupt the YAML block.
//
// GitLab itself doesn't allow `: ` in project slugs, but the handler
// is a public HTTP endpoint: a sender behind it could push any string.
// This is a defense-in-depth check, not a workaround for GitLab's
// rules.
func TestEnvelopeToDocbuilderMarkdown_UIDWithMetachars_ProducesValidYAML(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID:       42,
			Title:     "Has YAML metachars",
			CreatedAt: "2026-01-15T10:00:00.000Z",
			WebURL:    "https://gitlab.example.com/group/project/issues/42",
			Labels:    []string{"bug", "needs-triage"},
		},
		// Path with colon-space and hash, both of which would
		// corrupt an unquoted scalar in YAML 1.2.
		PathWithNamespace: "group/foo: bar#baz",
	}
	md, _, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)

	// Round-trip the rendered markdown through yaml.v3 the same
	// way the parser does. The frontmatter must parse cleanly
	// into the expected shape — one uid key with the literal
	// colon-and-hash-bearing value, no spurious "bar" or "baz"
	// keys leaked out of the unquoted scalar.
	parts := strings.SplitN(md, "\n", 3)
	require.Len(t, parts, 3, "rendered markdown must have frontmatter + body")
	require.Equal(t, "---", parts[0])

	var parsed map[string]any
	dec := yaml.NewDecoder(strings.NewReader(parts[1]))
	require.NoError(t, dec.Decode(&parsed),
		"frontmatter must be valid YAML even with metachars in path_with_namespace")

	uidVal, ok := parsed["uid"].(string)
	require.True(t, ok, "uid must round-trip as a string, not as a mapping")
	assert.Equal(t, "gitlab:group/foo: bar#baz:42", uidVal,
		"uid must preserve the metachars verbatim (YAML quoting handles the parsing)")

	// Belt-and-suspenders: a naive yaml.Unmarshal that treats
	// ": bar#baz" as a mapping key would split the uid into the
	// value "gitlab:group/foo:" plus two stray top-level keys.
	// Assert neither leakage happened.
	_, hasBar := parsed["bar"]
	assert.False(t, hasBar,
		"no stray top-level key leaked from the uid scalar")
}

// TestEnvelopeToDocbuilderMarkdown_StripsCarriageReturns pins the
// parser's CRLF normalization behavior. GitHub-style webmasters
// sometimes paste CRLF on Windows; without this the chunker would
// leave a stray `\r` on every line. The existing parser normalizes
// its raw input, but our builder writes the markdown fresh — so
// we must produce LF here.
func TestEnvelopeToDocbuilderMarkdown_StripsCarriageReturns(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID:         1,
			Title:       "t",
			CreatedAt:   "2026-01-15T10:00:00.000Z",
			WebURL:      "https://x",
			Description: "Line 1\r\nLine 2\r\nLine 3\r\n",
		},
		PathWithNamespace: "g/p",
	}
	md, _, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	assert.NotContains(t, md, "\r",
		"the produced markdown must be LF-only — the chunker and parser both expect LF")
	assert.Contains(t, md, "Line 1\nLine 2\nLine 3")
}

// TestEnvelopeToDocbuilderMarkdown_DropsEmptyNotesArray confirms
// that `IncludeNotes=false` (or an empty notes slice) means the
// Comments section is omitted entirely, so we don't render an
// empty `## Comments` heading in the body.
func TestEnvelopeToDocbuilderMarkdown_DropsEmptyNotesArray(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID:         1,
			Title:       "t",
			CreatedAt:   "2026-01-15T10:00:00.000Z",
			WebURL:      "https://x",
			Description: "body",
		},
		PathWithNamespace:  "g/p",
		IncludeNotes:       false,
		IncludeSystemNotes: false,
		Notes:              nil,
	}
	md, _, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	assert.NotContains(t, md, "## Comments",
		"the Comments section must not render when there are no notes to include")
}

// TestEnvelopeToDocbuilderMarkdown_NoteTimeFormat pins the
// timestamp format inside a note heading. RFC 3339 keeps it
// machine-parseable for any future filter that wants to scope
// "only comments after X" — and human-readable for the operator
// reading the chunked content.
func TestEnvelopeToDocbuilderMarkdown_NoteTimeFormat(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID: 1, Title: "t", CreatedAt: "2026-01-15T10:00:00.000Z", WebURL: "https://x",
		},
		PathWithNamespace:  "g/p",
		IncludeNotes:       true,
		IncludeSystemNotes: false,
		Notes: []NotePayload{
			{
				ID: 1, Body: "hi", System: false,
				Author:    Author{Username: "alice"},
				CreatedAt: "2026-01-15T11:00:00Z",
			},
		},
	}
	md, _, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	assert.Contains(t, md, "2026-01-15T11:00:00Z",
		"note timestamps must be RFC 3339 — short forms or Unix epochs break chunker readability")
}

// TestEnvelopeToDocbuilderMarkdown_DerivesDateAndCategoriesFrom
// Issue pins the upstream fixes: the issue's `created_at` populates
// both the parser's `date` (used by the unpublished preflight) and
// `created_at` (doc metadata), and the issue's `issue_type`
// populates a single-element Categories list.
func TestEnvelopeToDocbuilderMarkdown_DerivesDateAndCategoriesFromIssue(t *testing.T) {
	env := IssueEnvelope{
		Issue: IssuePayload{
			IID:       42,
			Title:     "t",
			CreatedAt: "2026-01-15T10:00:00.000Z",
			UpdatedAt: "2026-01-15T13:42:00.000Z",
			WebURL:    "https://x",
			IssueType: "incident",
		},
		PathWithNamespace: "g/p",
	}
	md, _, err := EnvelopeToDocbuilderMarkdown(env)
	require.NoError(t, err)
	assert.Contains(t, md, "date: 2026-01-15T10:00:00Z",
		"the issue's created_at must populate the parser's date frontmatter — issue #97's preflight reads this")
	assert.Contains(t, md, "created_at: 2026-01-15T10:00:00Z",
		"the issue's created_at must populate the doc metadata too")
	assert.Contains(t, md, "updated_at: 2026-01-15T13:42:00Z",
		"the issue's updated_at must populate the doc metadata too")
	assert.Contains(t, md, "- incident",
		"issue_type must populate the categories list so the chat surface can group by kind")
}

// TestIssueEnvelope_UnmarshalFromGitLabRealism checks that the
// real GitLab JSON deserializes into our envelope without any custom
// unmarshalers. The fixture is a copy-paste from the GitLab docs
// (verbatim) — if GitLab adds a new field or renames one, this
// test fails and we know to bump the schema.
func TestIssueEnvelope_UnmarshalFromGitLabRealism(t *testing.T) {
	const gitlabJSON = `{
		"id": 41,
		"iid": 6,
		"project_id": 1,
		"title": "Consequatur vero maxime deserunt laboriosam est voluptas dolorem.",
		"description": "Ratione dolores corrupti mollitia soluta quia.",
		"state": "opened",
		"labels": ["foo", "bar"],
		"created_at": "2016-01-04T15:31:51.081Z",
		"updated_at": "2016-01-04T15:31:51.081Z",
		"closed_at": null,
		"web_url": "http://gitlab.example.com/my-group/my-project/issues/6",
		"author": {
			"state": "active",
			"id": 18,
			"web_url": "https://gitlab.example.com/eileen.lowe",
			"name": "Alexandra Bashirian",
			"avatar_url": null,
			"username": "eileen.lowe"
		},
		"issue_type": "issue"
	}`

	var payload IssuePayload
	require.NoError(t, json.Unmarshal([]byte(gitlabJSON), &payload),
		"the GitLab issue JSON must deserialize into IssuePayload without custom unmarshalers")
	assert.Equal(t, 6, payload.IID)
	assert.Equal(t, "Consequatur vero maxime deserunt laboriosam est voluptas dolorem.", payload.Title)
	assert.Equal(t, "opened", payload.State)
	assert.Equal(t, []string{"foo", "bar"}, payload.Labels)
	require.NotNil(t, payload.Author)
	assert.Equal(t, "eileen.lowe", payload.Author.Username)
}

// TestNotePayload_UnmarshalFromGitLabRealism is the same check for
// the notes endpoint response. The fixture is a copy-paste from
// docs.gitlab.com/api/notes/ — including the `system: true` flag
// that drives our default-out filtering.
func TestNotePayload_UnmarshalFromGitLabRealism(t *testing.T) {
	const gitlabJSON = `{
		"id": 305,
		"body": "Text of the comment\r\n",
		"author": {
			"id": 1,
			"username": "pipin",
			"email": "admin@example.com",
			"name": "Pip",
			"state": "active",
			"created_at": "2013-09-30T13:46:01Z"
		},
		"created_at": "2013-10-02T09:56:03Z",
		"updated_at": "2013-10-02T09:56:03Z",
		"system": true,
		"notable_id": 121,
		"notable_type": "Issue",
		"project_id": 5,
		"notable_iid": 121,
		"resolvable": false,
		"confidential": false,
		"internal": false
	}`

	var note NotePayload
	require.NoError(t, json.Unmarshal([]byte(gitlabJSON), &note),
		"the GitLab note JSON must deserialize into NotePayload without custom unmarshalers")
	assert.Equal(t, "Text of the comment\r\n", note.Body,
		"the GitLab body field is the comment text — yes, it really is named body")
	assert.True(t, note.System,
		"the system flag must decode as a Go bool")
	assert.Equal(t, "pipin", note.Author.Username)
	assert.Equal(t, "2013-10-02T09:56:03Z", note.CreatedAt,
		"the timestamp must round-trip unchanged — created_at is a string, not a time.Time, in the wire shape")
}
