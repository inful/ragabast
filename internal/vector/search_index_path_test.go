package vector

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSearchIndexPathFor_EmptyVectorDir pins that the helper
// returns "" when the vector DB persistence dir is empty.
// Callers (NewService) detect this and skip index setup
// rather than passing an empty string to bleve.
func TestSearchIndexPathFor_EmptyVectorDir(t *testing.T) {
	assert.Empty(t, SearchIndexPathFor(""))
}

// TestSearchIndexPathFor_DefaultLayout pins the historical
// default: keyword index lives under vectorPersistenceDir/search.
// This is the path both stores share when keyword_index_dir
// is not explicitly configured.
func TestSearchIndexPathFor_DefaultLayout(t *testing.T) {
	assert.Equal(t,
		filepath.Join("/var/lib/ragabast/vectors", "search"),
		SearchIndexPathFor("/var/lib/ragabast/vectors"))
}

// TestResolveKeywordIndexPath_OverrideWins pins the new
// behavior introduced for issue #30: when keyword_index_dir
// is configured, that path wins regardless of the vector DB's
// persistence dir. Operators use this to put the keyword index
// on local disk while the vector DB sits on NFS.
func TestResolveKeywordIndexPath_OverrideWins(t *testing.T) {
	got := ResolveKeywordIndexPath(
		"/nfs/vectors",
		"/local/fast-disk/keyword",
	)
	assert.Equal(t, "/local/fast-disk/keyword", got)
}

// TestResolveKeywordIndexPath_FallsBackToDefault pins that an
// empty keyword_index_dir leaves the historical layout intact:
// keyword index at <vector_persistence_dir>/search. Operators
// who never set the override don't see any change.
func TestResolveKeywordIndexPath_FallsBackToDefault(t *testing.T) {
	got := ResolveKeywordIndexPath("/var/lib/ragabast/vectors", "")
	assert.Equal(t,
		filepath.Join("/var/lib/ragabast/vectors", "search"),
		got)
}

// TestResolveKeywordIndexPath_EmptyVectorDirEmptyOverride pins
// the misconfigured-pipeline contract: when both dirs are
// empty, the helper returns "" so callers can detect the
// misconfiguration and surface it (NewService logs and bails).
func TestResolveKeywordIndexPath_EmptyVectorDirEmptyOverride(t *testing.T) {
	got := ResolveKeywordIndexPath("", "")
	assert.Empty(t, got)
}
