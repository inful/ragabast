package service

import (
	"fmt"
	"regexp"
	"strings"

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
// the source's DocumentTitle (falling back to DocumentID if no
// title), and the link target is the source's DocbuilderURL
// (falling back to the first DocumentURLs entry, or bare text if
// no URL is configured).
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
		url := sourceLinkURL(sources[idx])
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
// link text for a cited source. Falls back to the document_id
// when no title is set so the user still sees *something*
// identifying.
func sourceLinkText(s models.SearchResult) string {
	if t := strings.TrimSpace(s.DocumentTitle); t != "" {
		return t
	}
	return s.DocumentID
}

// sourceLinkURL returns the URL to link to for a cited source.
// Preference order: DocbuilderURL (synthetic permalink set when
// ragabast.docbuilder_base_url is configured) → first user-set
// DocumentURL → empty string (caller emits bare text in that
// case).
func sourceLinkURL(s models.SearchResult) string {
	if s.DocbuilderURL != "" {
		return s.DocbuilderURL
	}
	if len(s.DocumentURLs) > 0 {
		return s.DocumentURLs[0]
	}
	return ""
}
