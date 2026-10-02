package parser

import (
	"strings"
	"time"

	"github.com/ragabast/internal/models"
)

// IsUnpublished reports whether the document should be filtered out
// of the embeddings based on Hugo's "don't publish" frontmatter
// markers. The caller passes the reference `now` so the helper is
// deterministic; production callers pass `time.Now()`, tests pass a
// fixed value.
//
// Rules (any one is enough to filter the document — OR semantics):
//
//   - Draft == true            — `draft: true` in frontmatter
//   - Date is set and future   — `date: <future>`
//   - PublishDate is set and future — `publishDate|pubdate|<published: <future>`
//   - ExpiryDate is set and past — `expiryDate|unpublishdate: <past>`
//
// Dates that fail to parse stay at the zero value in the Document;
// zero dates are ignored by this helper (a typo in `date:` does not
// accidentally filter a document — we err on the side of letting
// things through).
//
// A nil document is treated as "published": the helper never crashes
// and never returns "yes" on a nil.
func IsUnpublished(doc *models.Document, now time.Time) bool {
	if doc == nil {
		return false
	}
	if doc.Draft {
		return true
	}
	if !doc.Date.IsZero() && doc.Date.After(now) {
		return true
	}
	if !doc.PublishDate.IsZero() && doc.PublishDate.After(now) {
		return true
	}
	if !doc.ExpiryDate.IsZero() && doc.ExpiryDate.Before(now) {
		return true
	}
	return false
}

// lowerHugoKeys returns a copy of fm with every key lowercased. The
// returned map is independent of fm — the caller can mutate it
// without touching the original.
//
// This exists because yaml.v3 is case-sensitive on map keys when
// unmarshaling into map[string]any: `publishDate:` and `publishdate:`
// would produce two different keys. Hugo itself is case-insensitive
// on field names, so the parser normalizes keys for the four "don't
// publish" fields and their aliases (publishdate, pubdate,
// published, expirydate, unpublishdate) before looking them up.
//
// The four Hugo fields this function exists for are well-known to
// the parser layer; we don't lowercase the entire frontmatter map
// because every other field (fingerprint, uid, title, tags, …) is
// read with a single fixed-case lookup and lowercasing them would
// silently break if the existing tests pin case-sensitive behavior.
func lowerHugoKeys(fm map[string]any) map[string]any {
	out := make(map[string]any, len(fm))
	for k, v := range fm {
		out[strings.ToLower(k)] = v
	}
	return out
}

// parseHugoTime coerces a YAML frontmatter value to time.Time. Hugo
// accepts RFC 3339 timestamps; we also tolerate the `time.Time` value
// yaml.v3 builds from an unquoted ISO 8601 string. Any other shape
// (int, bool, missing, malformed string) returns the second return as
// false — the caller treats that as "ignore this field".
func parseHugoTime(v any) (time.Time, bool) {
	switch tv := v.(type) {
	case time.Time:
		return tv, true
	case string:
		if t, err := time.Parse(time.RFC3339, tv); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// firstHugoTime walks the (lower-cased) frontmatter map in the given
// key order and returns the first key whose value parses as a
// time.Time. Returns the zero time if no key matches.
//
// The "first wins" order matches Hugo's own lookup order: when more
// than one alias is present, the canonical name wins. Operators who
// set both `publishDate:` and `pubdate:` will see publishDate honored.
func firstHugoTime(fm map[string]any, keys ...string) time.Time {
	for _, k := range keys {
		if v, ok := fm[k]; ok {
			if t, ok := parseHugoTime(v); ok {
				return t
			}
		}
	}
	return time.Time{}
}
