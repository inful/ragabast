package vector

import (
	"strings"

	"github.com/ragabast/internal/models"
)

// inferSourceKind picks a SourceKind for a chunk coming out of the
// vector store, given the persisted "source_kind" metadata entry
// (if any) and the chunk's UID. Used by the SearchResult builder
// in db.go so the citation dispatch and the post-filter can branch
// on the kind without re-implementing the same logic.
//
// Rule (matches the spec):
//
//  1. If metaKind is non-empty and a known kind, trust it.
//  2. Else if uid starts with "gitlab:", infer SourceGitLab.
//  3. Else infer SourceDocbuilder (the historical single-source
//     default — keeps pre-SourceKind corpora working).
//
// Step 1's "and a known kind" matters: a malformed or future kind
// (e.g., an early "redmine" string that hasn't shipped yet) is
// treated as untrusted and falls through to the prefix rule, so
// we never persist a SourceKind the rest of the code doesn't know
// how to dispatch on. TestInferSourceKind pins each branch.
func inferSourceKind(metaKind, uid string) models.SourceKind {
	if metaKind != "" {
		k := models.SourceKind(metaKind)
		if k.IsValid() {
			return k
		}
	}
	if strings.HasPrefix(uid, "gitlab:") {
		return models.SourceGitLab
	}
	return models.SourceDocbuilder
}
