package service

import (
	"fmt"
	"regexp"

	"github.com/ragabast/internal/models"
)

// sourceMarkerRegex matches the LLM's inline citation syntax:
// [src:0], [src:1], ..., where the integer is the zero-based
// index into the sources slice the prompt assembly handed the
// model. The regex is intentionally specific to "[src:N]" so
// it does not eat other bracket patterns like "[1]" (the older
// prompt syntax) or "[2025]" (year references).
//
// The regex matches greedily on the integer; out-of-range indices
// are surfaced to the caller as a still-formatted "[src:N]"
// token, so a model that emits an erroneous index shows up in the
// output rather than being silently swallowed.
var sourceMarkerRegex = regexp.MustCompile(`\[src:(\d+)\]`)

// InlineSourceLinks replaces each [src:N] marker in the LLM's
// reply with a markdown link to the Nth source. The link text is
// the source's DisplayLabel (title → filename → document_id), and
// the link target is the source's DocbuilderURL (falling back to
// the first DocumentURLs entry, or bare text if no URL is
// configured).
//
// Index out of range: the marker is left in place (e.g. "[src:7]")
// so the user (and any future debugging) sees what the model tried
// to cite.
//
// Empty sources: the input is returned unchanged.
//
// The function is intentionally simple — a single regex pass —
// so it costs nothing on the common case of a model that emits
// zero markers. The chat handler runs this before the markdown
// renderer, so the resulting markdown links become clickable <a>
// tags in the rendered HTML.
func InlineSourceLinks(answer string, sources []models.SearchResult) string {
	if !sourceMarkerRegex.MatchString(answer) || len(sources) == 0 {
		return answer
	}
	return sourceMarkerRegex.ReplaceAllStringFunc(answer, func(match string) string {
		// ReplaceAllStringFunc gives us the full match; we still
		// have to parse the int out of it.
		submatch := sourceMarkerRegex.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		var idx int
		if _, err := fmt.Sscanf(submatch[1], "%d", &idx); err != nil {
			return match
		}
		if idx < 0 || idx >= len(sources) {
			// Out-of-bounds: leave the marker visible so the user
			// (and any debugging) can see what the model tried
			// to do.
			return match
		}
		text := sourceLinkText(sources[idx])
		url := SourceLinkURL(sources[idx])
		if url == "" {
			// No URL anywhere — emit bare title text so the
			// reference is still visible to the user. An empty
			// markdown link ("[title]()") would render as the
			// literal text anyway, but the bare form is cleaner.
			return text
		}
		return fmt.Sprintf("[%s](%s)", text, url)
	})
}

// sourceLinkText returns the human-readable label to use as the
// link text for a cited source. Delegates to SearchResult.DisplayLabel,
// which falls back through title → filename (basename without
// extension) → document_id so the user always sees something
// identifying. The filename fallback matters for documents that
// have no H1 (so no title) but were ingested from disk — the
// on-disk name is more useful than the UUID.
func sourceLinkText(s models.SearchResult) string {
	return s.DisplayLabel()
}

// SourceLinkURL returns the URL to link to for a cited source
// or "direct link" in the sources panel. The choice is
// per-source-kind:
//
//   - SourceGitLab: DocumentURLs[0] (the original GitLab web_url),
//     falling back to DocbuilderURL when the operator didn't
//     provide a URL. The bug surfaced during review of the
//     handlers-split refactor was that EVERY citation resolved to
//     DocbuilderURL when ragabast.docbuilder_base_url was set,
//     sending operators to the synthetic docbuilder permalink
//     instead of the original GitLab issue. GitLab sources must
//     therefore prefer the user-set DocumentURL.
//   - SourceDocbuilder / SourceUnknown: DocbuilderURL →
//     DocumentURLs[0] (the historical behavior, preserved for
//     backwards compatibility — pre-SourceKind corpora serialize
//     back to SourceUnknown and must continue to land on the
//     docbuilder permalink).
//
// Returns "" when neither field has a value; the caller emits
// bare title text in that case (see InlineSourceLinks).
//
// SourceKind is populated by the vector layer (see
// inferSourceKind in internal/vector); pre-SourceKind corpora
// reach this function with SourceUnknown and follow the
// docbuilder branch, so existing citations keep working.
//
// Exported (capital S) so the chat handler can pre-populate
// SearchResult.CitationURL for the sources-panel template,
// keeping the per-kind dispatch logic out of the template.
func SourceLinkURL(s models.SearchResult) string {
	switch s.SourceKind {
	case models.SourceGitLab:
		if len(s.DocumentURLs) > 0 {
			return s.DocumentURLs[0]
		}
		if s.DocbuilderURL != "" {
			return s.DocbuilderURL
		}
		return ""
	case models.SourceDocbuilder, models.SourceUnknown:
		// Historical behavior: prefer DocbuilderURL → DocumentURLs[0].
		// SourceUnknown (zero value) lands here so pre-SourceKind
		// corpora keep resolving to the docbuilder permalink.
		if s.DocbuilderURL != "" {
			return s.DocbuilderURL
		}
		if len(s.DocumentURLs) > 0 {
			return s.DocumentURLs[0]
		}
		return ""
	default:
		// Future SourceKind constants fall back to the docbuilder
		// behavior until they get an explicit case. Returning ""
		// here is not reachable today (every known kind is matched
		// above) but keeps the switch exhaustive for any new
		// constant that lands before this file is updated.
		return ""
	}
}
