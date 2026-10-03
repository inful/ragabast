// Package gitlab converts GitLab issue JSON envelopes into
// docbuilder YAML-frontmatter markdown that the existing ragabast
// ingest pipeline accepts unchanged.
//
// Ragabast is a pure receiver of ingestion requests. An external
// sender (a CI job, a scheduled `curl`, a home-grown admin script)
// is responsible for fetching from the GitLab REST API and
// POSTing the result here. This package owns the on-the-wire
// shape: the JSON struct tags mirror the GitLab API field names
// verbatim, so the sender can `curl ... | jq . | curl ...` without
// any translation. We then translate internally to docbuilder
// YAML, which goes through the existing parser / chunker / vector
// pipeline unchanged.
//
// Future GitLab shapes (merge requests, wiki pages, work items)
// follow the same pattern: a Go struct mirroring the wire JSON
// and a pure function that emits docbuilder markdown. The wire
// contract is owned in this package; the existing pipeline owns
// the storage contract.
package gitlab

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Author is the GitLab `author` field on issues and notes. We
// keep just the three fields the body markdown cares about
// (display name, username); the rest of the fields GitLab
// returns (`web_url`, `avatar_url`, `state`) are dropped because
// they don't improve retrieval.
//
// Joining name is a real GitLab field name. We don't rename it.
type Author struct {
	ID        int    `json:"id,omitempty"`
	Username  string `json:"username,omitempty"`
	Name      string `json:"name,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	State     string `json:"state,omitempty"`
	Email     string `json:"email,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// IssuePayload mirrors the JSON returned by
// `GET /api/v4/projects/:id/issues/:issue_iid`. Field names and
// JSON tags match the live API verbatim — see the example
// responses in docs.gitlab.com/api/issues/. Optional fields are
// tagged with `,omitempty` so the sender can send `{issue: iid,
// title: t, ...}` for the smallest cohort.
type IssuePayload struct {
	ID               int      `json:"id"`
	IID              int      `json:"iid"`
	ProjectID        int      `json:"project_id"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	State            string   `json:"state"`
	Labels           []string `json:"labels"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	ClosedAt         string   `json:"closed_at,omitempty"`
	WebURL           string   `json:"web_url"`
	Author           Author   `json:"author"`
	IssueType        string   `json:"issue_type,omitempty"`
	Confidential     bool     `json:"confidential,omitempty"`
	DiscussionLocked bool     `json:"discussion_locked,omitempty"`
	UserNotesCount   int      `json:"user_notes_count,omitempty"`
	// We accept but drop the fields below. The sender can include
	// them; we don't propagate them to the body because they
	// don't improve retrieval. The struct tags keep the wire shape
	// future-proof: if GitLab adds a new field, the JSON still
	// unmarshals cleanly into a struct field we name.
	Assignees  json.RawMessage `json:"assignees,omitempty"`
	Milestone  json.RawMessage `json:"milestone,omitempty"`
	DueDate    string          `json:"due_date,omitempty"`
	References json.RawMessage `json:"references,omitempty"`
	Links      json.RawMessage `json:"_links,omitempty"`
	Severity   string          `json:"severity,omitempty"`
}

// NotePayload mirrors the JSON returned by
// `GET /api/v4/projects/:id/issues/:issue_iid/notes`. The body
// field is the comment text — yes, GitLab really does call the
// comment text body. We don't rename it.
type NotePayload struct {
	ID           int    `json:"id"`
	Body         string `json:"body"`
	Author       Author `json:"author"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	System       bool   `json:"system"`
	NoteableID   int    `json:"notable_id,omitempty"`
	NoteableType string `json:"notable_type,omitempty"`
	NoteableIID  int    `json:"notable_iid,omitempty"`
	ProjectID    int    `json:"project_id,omitempty"`
	Resolvable   bool   `json:"resolvable,omitempty"`
	Confidential bool   `json:"confidential,omitempty"`
	Internal     bool   `json:"internal,omitempty"`
}

// IssueEnvelope is the wire payload for
// POST /api/ingest/gitlab/issue. The Issue field is a verbatim
// copy of the GitLab issue JSON; the other fields are
// ragabast-side knobs.
type IssueEnvelope struct {
	Issue IssuePayload `json:"issue"`
	// PathWithNamespace is the project's human-readable slug
	// like "group/bar". GitLab's issue JSON has only the numeric
	// project_id, which is unique per GitLab *instance* — but
	// a single sender could push from two different instances,
	// so we ask for the slug separately to make the ragabast
	// UID globally unique across sources.
	PathWithNamespace string `json:"path_with_namespace"`
	// IncludeNotes defaults to false. When true, the rendered
	// body has a `## Notes` section listing each note.
	IncludeNotes bool `json:"include_notes"`
	// IncludeSystemNotes defaults to false. When false, notes
	// with `system: true` (the "closed", "changed milestone
	// to X" records) are filtered out before rendering.
	IncludeSystemNotes bool `json:"include_system_notes"`
	// Notes is the array of GitLab note JSON. The sender is
	// responsible for fetching from
	// /api/v4/projects/:id/issues/:issue_iid/notes and supplying
	// the result here.
	Notes []NotePayload `json:"notes"`
}

// noteRendered is the on-disk representation of a single note.
// We sort notes by created_at and render each as a level-3
// heading with the author handle and timestamp — that gives the
// chunker a clean split on the `###` boundary.
type noteRendered struct {
	heading string
	body    string
}

// EnvelopeToDocbuilderMarkdown converts a GitLab issue envelope
// into docbuilder YAML-frontmatter markdown. The output is what
// Service.IngestDocument expects, so the call goes through the
// existing parser, unpublished preflight, chunker, and vector
// ingest pipeline unchanged.
//
// Returns:
//   - the rendered markdown
//   - the count of system notes that were filtered out (so the
//     handler can log it for the operator — the system-note
//     policy is observable)
//   - an error if a UID/slug is missing or required-field
//     validation fails
//
// The function is pure: no I/O, no clock, no logging. Side
// effects (logging the system-note count, writing to the vector
// store) live in the handler.
func EnvelopeToDocbuilderMarkdown(env IssueEnvelope) (string, int, error) {
	if env.Issue.IID == 0 {
		return "", 0, fmt.Errorf("gitlab: issue.iid is required (path=%q, title=%q)",
			env.PathWithNamespace, env.Issue.Title)
	}
	if strings.TrimSpace(env.PathWithNamespace) == "" {
		return "", 0, fmt.Errorf("gitlab: path_with_namespace is required (iid=%d)",
			env.Issue.IID)
	}

	uid := fmt.Sprintf("gitlab:%s:%d", env.PathWithNamespace, env.Issue.IID)

	body := strings.ReplaceAll(env.Issue.Description, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	var b strings.Builder
	b.WriteString("---\n")
	writeFrontmatter(&b, env, uid)

	// Docbuilder convention: an absent `fingerprint:` line
	// tells the parser to compute the fingerprint from the body
	// content. Computing here would require importing it; better
	// to let the parser do it so the conversion is symmetric
	// with what an operator would write.
	b.WriteString("---\n\n")
	body = "# " + env.Issue.Title + "\n\n" + body

	rendered, sysFiltered := renderNotes(env)
	if len(rendered) > 0 {
		body += "\n\n## Comments\n\n"
		var bodySb187 strings.Builder
		for _, n := range rendered {
			bodySb187.WriteString("### " + n.heading + "\n\n" + n.body + "\n\n")
		}
		body += bodySb187.String()
	}

	// Final markdown: frontmatter + body. The leading `# <title>`
	// makes the chunker split on the issue title as the H1; the
	// `## Comments` heading splits the notes into their own
	// hierarchy. Trim trailing whitespace so the parser doesn't
	// surface a stray empty trailing chunk.
	b.WriteString(strings.TrimRight(body, " \t\n"))
	return b.String(), sysFiltered, nil
}

// writeFrontmatter emits the frontmatter block (excluding the
// leading/trailing `---`). Field order is fixed so a future
// contributor who adds a new field doesn't accidentally re-shuffle
// the existing ones. (Frontmatter field order does not affect
// YAML semantics, but it does affect diff readability.)
func writeFrontmatter(b *strings.Builder, env IssueEnvelope, uid string) {
	// The uid is "gitlab:{path}:{iid}" — a sender-supplied
	// path_with_namespace containing `: `, `#`, or a newline
	// would otherwise corrupt the frontmatter (yaml would parse
	// ": bar" as a new mapping key, splitting the uid across two
	// top-level keys). sanitizeForYAML quotes such values; we apply
	// it to every string that lands in frontmatter, the uid
	// included, so a crafted sender can't break the YAML block.
	b.WriteString("uid: " + sanitizeForYAML(uid) + "\n")
	b.WriteString("title: " + sanitizeForYAML(env.Issue.Title) + "\n")

	// source_kind tags this document so the citation dispatch
	// (per-kind URL preference) and the source-kind post-filter
	// (per-kind scoping) can branch correctly. See .planning/
	// multi-source.md for the rationale; the wire value must
	// match models.SourceGitLab verbatim.
	b.WriteString("source_kind: gitlab\n")

	// Tags. We always emit the two discovery-affordance tags
	// first ("gitlab-issue", "gitlab:<path>") so the chat
	// surface can filter on them uniformly with other source
	// kinds. Issue labels come last so a future tag-filter
	// PR that wants to scan for user-side labels has them in
	// a predictable position.
	b.WriteString("tags:\n")
	b.WriteString("  - gitlab-issue\n")
	b.WriteString("  - gitlab:" + sanitizeForYAML(env.PathWithNamespace) + "\n")
	for _, label := range env.Issue.Labels {
		b.WriteString("  - " + sanitizeForYAML(label) + "\n")
	}

	// URLs.
	b.WriteString("urls:\n")
	b.WriteString("  - " + sanitizeForYAML(env.Issue.WebURL) + "\n")

	// Categories: a single-element list with the issue type.
	if env.Issue.IssueType != "" {
		b.WriteString("categories:\n")
		b.WriteString("  - " + sanitizeForYAML(env.Issue.IssueType) + "\n")
	}

	// Date fields. The parser reads `date:` for the unpublished
	// preflight (issue #97) and `created_at:` / `updated_at:`
	// for the doc metadata. We populate all three from the
	// GitLab timestamps; the parser tolerates missing fields.
	if env.Issue.CreatedAt != "" {
		b.WriteString("date: " + normalizeRFC3339(env.Issue.CreatedAt) + "\n")
		b.WriteString("created_at: " + normalizeRFC3339(env.Issue.CreatedAt) + "\n")
	}
	if env.Issue.UpdatedAt != "" {
		b.WriteString("updated_at: " + normalizeRFC3339(env.Issue.UpdatedAt) + "\n")
	}

	// Source-specific metadata. Emitted only when populated so
	// a closed issue with no author still round-trips cleanly
	// through the parser's "extra fields → chunk.Metadata" pass.
	// The keys here are the wire contract that downstream
	// filters rely on (see internal/vector/metadata.go for the
	// post-filter side).
	if env.Issue.State != "" {
		b.WriteString(MetadataKeyState + ": " + sanitizeForYAML(env.Issue.State) + "\n")
	}
	if env.Issue.Author.Username != "" {
		b.WriteString(MetadataKeyAuthorUsername + ": " +
			sanitizeForYAML(env.Issue.Author.Username) + "\n")
	}

	// file_path is what the chat surface falls back to when a
	// doc has no title (DisplayLabel in models/display.go).
	// GitLab issues always have one, but emitting a meaningful
	// file_path keeps the fallback consistent: the operator
	// sees "gitlab:group/bar#42" rather than a bare document id.
	b.WriteString("file_path: gitlab:" + sanitizeForYAML(env.PathWithNamespace) + "#" +
		strconv.Itoa(env.Issue.IID) + "\n")
}

// sanitizeForYAML guards against the small set of YAML metacharacters
// that would corrupt the frontmatter. We escape the values that
// operators have most likely entered directly into a YAML field
// (title, label, slug, URL). The parser also accepts the same
// content as a string, so we keep the conversion narrow.
func sanitizeForYAML(s string) string {
	if !strings.ContainsAny(s, ":#\n") {
		return s
	}
	// Wrap in double-quotes; escape any embedded double-quotes
	// and backslashes per the YAML 1.2 spec.
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// normalizeRFC3339 trims any sub-second precision GitLab may
// include. The docbuilder parser uses time.Parse(time.RFC3339)
// which requires the second-precision form — `2016-01-04T15:31:46.176Z`
// parses as RFC 3339 but the parser is lenient; we normalize
// anyway so the rendered markdown is consistent across issues.
func normalizeRFC3339(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Fall back to the second-precision form. If that
		// also fails, return the input verbatim — the parser
		// will surface a 400 with the right error.
		t2, err2 := time.Parse(time.RFC3339, s)
		if err2 != nil {
			return s
		}
		return t2.Format(time.RFC3339)
	}
	return t.UTC().Format(time.RFC3339)
}

// renderNotes sorts notes by created_at, filters out system notes
// when the policy is default-off, and returns the rendered list
// along with how many system notes were dropped.
func renderNotes(env IssueEnvelope) ([]noteRendered, int) {
	if !env.IncludeNotes || len(env.Notes) == 0 {
		return nil, 0
	}

	type withTime struct {
		raw  NotePayload
		time time.Time
	}
	withTimes := make([]withTime, 0, len(env.Notes))
	for _, n := range env.Notes {
		t, _ := time.Parse(time.RFC3339, n.CreatedAt)
		withTimes = append(withTimes, withTime{raw: n, time: t})
	}
	sort.SliceStable(withTimes, func(i, j int) bool {
		// Notes that fail to parse sort to the end (zero time)
		// so a single malformed note doesn't block the rest of
		// the conversation from view.
		if withTimes[i].time.IsZero() && !withTimes[j].time.IsZero() {
			return false
		}
		if !withTimes[i].time.IsZero() && withTimes[j].time.IsZero() {
			return true
		}
		return withTimes[i].time.Before(withTimes[j].time)
	})

	sysFiltered := 0
	renderedOut := make([]noteRendered, 0, len(withTimes))
	for _, n := range withTimes {
		if n.raw.System && !env.IncludeSystemNotes {
			sysFiltered++
			continue
		}
		renderedOut = append(renderedOut, noteRendered{
			heading: "@" + pickNoteAuthor(n.raw.Author) + " — " + normalizeRFC3339(n.raw.CreatedAt),
			body:    n.raw.Body,
		})
	}
	return renderedOut, sysFiltered
}

// pickNoteAuthor prefers Username (handle-style, e.g. "@alice")
// over Name (display, e.g. "@Alexandra Bashirian"). The chat
// surface already uses @-handles for the operator UI, and
// handles are stable across display-name changes.
func pickNoteAuthor(a Author) string {
	if a.Username != "" {
		return a.Username
	}
	return a.Name
}

// Metadata keys written into the rendered frontmatter and
// subsequently copied to chunk.Metadata by the parser. These are
// the wire contract for source-specific filters (e.g. "only
// open issues" → MetadataKeyState == "opened"). Centralized so
// typos surface as compile errors within the gitlab package
// instead of as silent filter misses at retrieval time.
const (
	// MetadataKeyState carries the issue state ("opened",
	// "closed"). The vector layer does not interpret the value;
	// it's surfaced as-is so future filters can extend (e.g.
	// "locked", "merged" for non-issue kinds).
	MetadataKeyState = "state"

	// MetadataKeyAuthorUsername carries the author's GitLab
	// handle. Stable across display-name changes; chat surfaces
	// render it as @-handles.
	MetadataKeyAuthorUsername = "author_username"
)
