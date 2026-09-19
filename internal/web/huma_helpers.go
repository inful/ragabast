package web

import (
	"strings"

	"github.com/ragabast/internal/models"
)

// normalizeStringSlice converts a []any (the shape YAML/JSON
// unmarshal produces for arrays) into a []string of trimmed,
// non-empty values. Order is preserved.
func normalizeStringSlice(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// uniqueAppend returns dst with src appended, deduplicated by
// trimmed value. Empties from src are skipped.
func uniqueAppend(dst []string, src []string) []string {
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, v := range dst {
		seen[v] = struct{}{}
	}
	for _, v := range src {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		dst = append(dst, v)
	}
	return dst
}

// normalizeTagsLower trims, lowercases, and dedupes a slice of
// tags. Order is preserved by first appearance.
func normalizeTagsLower(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		v = strings.ToLower(v)
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// canonicalizeCategories rewrites each value to its allowed-list
// form (case-insensitive match, allowed-list casing wins) and
// dedupes. Preserves input order.
func canonicalizeCategories(values []string, allowed []string) []string {
	allow := make(map[string]string, len(allowed))
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		allow[strings.ToLower(a)] = a
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		canonical, ok := allow[strings.ToLower(v)]
		if ok {
			v = canonical
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// filterAllowed keeps only values that match an entry in allowed
// (case-insensitive), rewriting each kept value to its allowed-list
// form. Order is preserved.
func filterAllowed(values []string, allowed []string) []string {
	allow := make(map[string]string, len(allowed))
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		allow[strings.ToLower(a)] = a
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		canonical, ok := allow[strings.ToLower(v)]
		if !ok {
			continue
		}
		out = append(out, canonical)
	}
	return uniqueNonEmptyStrings(out)
}

// splitAllowedAndCustomTags partitions suggested into the tags
// that match the allowed list (rewritten to allowed casing) and
// the tags that don't (passed through as-is). Both result slices
// are deduped.
func splitAllowedAndCustomTags(suggested []string, allowed []string) (allowedTags []string, customTags []string) {
	allow := make(map[string]string, len(allowed))
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		allow[strings.ToLower(a)] = a
	}
	for _, v := range uniqueNonEmptyStrings(suggested) {
		canonical, ok := allow[strings.ToLower(v)]
		if ok {
			allowedTags = append(allowedTags, canonical)
			continue
		}
		customTags = append(customTags, v)
	}
	return uniqueNonEmptyStrings(allowedTags), uniqueNonEmptyStrings(customTags)
}

// uniqueNonEmptyStrings dedupes after trimming. Preserves first-seen order.
func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		norm := strings.TrimSpace(v)
		if norm == "" {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	return out
}

// planPruneByExplicitDelete partitions deleteIDs into those that
// exist in docsByID (toDelete) and those that don't (notFound).
// Input is deduped; output order matches the deduped input order.
func planPruneByExplicitDelete(docsByID map[string]models.DocumentInfo, deleteIDs []string) (toDelete []string, notFound []string) {
	ids := uniqueNonEmptyStrings(deleteIDs)
	toDelete = make([]string, 0, len(ids))
	notFound = make([]string, 0, 4)
	for _, documentID := range ids {
		if _, ok := docsByID[documentID]; !ok {
			notFound = append(notFound, documentID)
			continue
		}
		toDelete = append(toDelete, documentID)
	}
	return toDelete, notFound
}

// planPruneByKeepList partitions docs into kept (matches the keep
// list by ID or UID) and toDelete (everything else). Order
// preserved by document-list order.
func planPruneByKeepList(docs []models.DocumentInfo, keepDocumentIDs []string, keepUIDs []string) (toDelete []string, kept []string) {
	keepIDs := make(map[string]struct{}, len(keepDocumentIDs))
	for _, documentID := range uniqueNonEmptyStrings(keepDocumentIDs) {
		keepIDs[documentID] = struct{}{}
	}
	keepByUID := make(map[string]struct{}, len(keepUIDs))
	for _, uid := range uniqueNonEmptyStrings(keepUIDs) {
		keepByUID[uid] = struct{}{}
	}

	toDelete = make([]string, 0, len(docs))
	kept = make([]string, 0, len(docs))
	for _, d := range docs {
		_, keepByID := keepIDs[d.ID]
		_, keepUID := keepByUID[d.UID]
		if keepByID || keepUID {
			kept = append(kept, d.ID)
			continue
		}
		toDelete = append(toDelete, d.ID)
	}

	return toDelete, kept
}
