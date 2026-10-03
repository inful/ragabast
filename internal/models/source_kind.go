package models

import "html/template"

// SourceKind identifies where a document (and its chunks / search
// results) came from. The set of known kinds is closed today
// (docbuilder + GitLab) but the model is intentionally extensible:
// each new ingest source gets its own constant here, and downstream
// layers (citation dispatch, search filter, presentation grouping)
// branch on the kind.
//
// Wire format: the constant string values are persisted in BSON
// (via models.Chunk.SourceKind) and surfaced in JSON exports. They
// are part of the wire contract — renaming a constant is a breaking
// change for any existing vector store.
//
// SourceUnknown is intentionally the zero-value empty string so
// chunks ingested before SourceKind existed serialize back to
// "no kind" and downstream code can default them to SourceDocbuilder
// (the historical single-source behavior). Don't change this.
type SourceKind string

const (
	// SourceUnknown is the zero-value default. Treated as
	// SourceDocbuilder by the citation dispatch and search
	// filter so existing corpora keep working without a
	// backfill.
	SourceUnknown SourceKind = ""

	// SourceDocbuilder marks a document/chunk that came from the
	// docbuilder markdown ingest path. Today the only producer
	// is internal/web/huma_ingest.go and the async batch path.
	SourceDocbuilder SourceKind = "docbuilder"

	// SourceGitLab marks a document/chunk that came from the
	// GitLab issue ingest path. The producer is
	// internal/web/huma_ingest_gitlab.go, and the UID prefix is
	// `gitlab:{path}:{iid}`.
	SourceGitLab SourceKind = "gitlab"
)

// IsValid reports whether s is one of the known SourceKind values.
// Used by parsers, the chat handler form-input validator, and any
// future persistence layer that wants to reject garbage instead of
// silently persisting it.
//
// The zero value (empty string) is intentionally considered valid
// because it is the backwards-compatible default — see the
// SourceUnknown doc comment.
func (s SourceKind) IsValid() bool {
	switch s {
	case SourceUnknown, SourceDocbuilder, SourceGitLab:
		return true
	}
	return false
}

// SourceIcon returns a small HTML marker identifying the source
// kind in chat citations and the search-results page. Returned as
// template.HTML so it can be dropped directly into chat_message.html
// without a second escaping pass — the surrounding html/template
// is responsible for context-aware escaping, but the helper itself
// is a fixed string with no untrusted input.
//
// The marker is intentionally compact (a span with a kind-name
// class and a kind-name text node) so future styling work can
// pick it up via CSS without touching Go. The kind name appears
// as text inside the span so a11y tools, screen readers, and
// debugging all surface the source identity without parsing
// surrounding HTML.
//
// SourceUnknown returns an empty marker so pre-SourceKind
// corpora don't display a misleading badge. The chat template
// wraps SourceIcon in {{ if (call .SourceIcon) }} so an empty
// marker renders as nothing.
//
// When adding a new SourceKind constant, add a case here AND
// add the constant to IsValid above — the exhaustive linter
// catches missing cases at compile time.
func (s SourceKind) SourceIcon() template.HTML {
	switch s {
	case SourceGitLab:
		return template.HTML(`<span class="source-icon source-icon-gitlab" title="GitLab issue">gitlab</span>`)
	case SourceDocbuilder:
		return template.HTML(`<span class="source-icon source-icon-docbuilder" title="Document">doc</span>`)
	case SourceUnknown:
		return template.HTML("")
	default:
		// Future SourceKind constants land here as a safety net;
		// the exhaustive linter flags new constants that fall
		// through without their own case.
		return template.HTML("")
	}
}

// DisplayName returns the human-readable name for the source
// kind, used in chat sources panel section headers ("GitLab (3)"
// rather than the wire-value "gitlab (3)"). The wire format
// (the SourceKind constant value) stays lowercase; this is the
// presentation surface only.
//
// SourceUnknown returns "" so pre-SourceKind chunks don't show
// a misleading section header. The chat handler's
// sourcesByKind helper skips empty groups, so a single
// SourceUnknown chunk produces no group at all.
func (s SourceKind) DisplayName() string {
	switch s {
	case SourceGitLab:
		return "GitLab"
	case SourceDocbuilder:
		return "Docbuilder"
	case SourceUnknown:
		return ""
	default:
		return ""
	}
}

// InlineSourceIcon returns a short Unicode glyph suitable for
// embedding inside the markdown link text produced by
// InlineSourceLinks. Markdown (CommonMark) treats inline HTML
// tags inside link text as literal characters, so a <span>-
// based icon can't survive the markdown rendering pass without
// a custom AST node. A Unicode glyph passes through cleanly.
//
// Why a per-kind glyph at all: the inline `[src:N]` link in the
// answer body is what an operator scans to see "the LLM drew
// from X". A leading emoji disambiguates the citation's origin
// at a glance without requiring a separate metadata block.
//
// SourceUnknown returns "" so pre-SourceKind chunks don't show
// a misleading glyph. The caller is responsible for trimming
// the leading space when the glyph is empty.
func (s SourceKind) InlineSourceIcon() string {
	switch s {
	case SourceGitLab:
		return "🦊"
	case SourceDocbuilder:
		return "📄"
	case SourceUnknown:
		return ""
	default:
		return ""
	}
}
