package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSourceKind_Constants pins the wire values of the SourceKind
// enum. The string values are persisted in BSON and surfaced in the
// chunk metadata; changing them is a wire-format break for any
// existing vector store and any future source ingester that mirrors
// these strings in its frontmatter / tags. Anchor the constants
// here so a future contributor who renames them gets a clear test
// failure.
func TestSourceKind_Constants(t *testing.T) {
	require.Equal(t, SourceUnknown, SourceKind(""),
		"SourceUnknown must be the zero-value empty string for backwards compatibility with pre-SourceKind chunks")
	require.Equal(t, SourceDocbuilder, SourceKind("docbuilder"),
		"SourceDocbuilder wire value is 'docbuilder' (used in bson and any future JSON exports)")
	require.Equal(t, SourceGitLab, SourceKind("gitlab"),
		"SourceGitLab wire value is 'gitlab' (matches the UID prefix convention from internal/gitlab/payload.go)")
}

// TestSourceKind_IsValid covers the validation contract. The chat
// handler reads `source_kinds` from the form; IsValid lets it reject
// garbage without reaching the service layer. Known kinds return
// true; anything else returns false.
func TestSourceKind_IsValid(t *testing.T) {
	cases := []struct {
		name string
		in   SourceKind
		want bool
	}{
		{name: "unknown zero value", in: SourceUnknown, want: true},
		{name: "docbuilder", in: SourceDocbuilder, want: true},
		{name: "gitlab", in: SourceGitLab, want: true},
		{name: "empty string is unknown", in: SourceKind(""), want: true},
		{name: "bogus value", in: SourceKind("redmine"), want: false},
		{name: "case sensitive", in: SourceKind("GitLab"), want: false},
		{name: "whitespace", in: SourceKind(" gitlab"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.in.IsValid())
		})
	}
}

// TestNewDocument_DefaultsIncludeSourceKind pins the backwards-
// compatibility contract: documents constructed via NewDocument
// (which is what the docbuilder ingest writer uses) must have
// SourceKind == SourceUnknown so the zero-value defaulting in
// downstream code (citation dispatch, search filter) treats them
// as docbuilder per the spec. Without this test, a future
// contributor could silently initialize SourceKind = "docbuilder"
// and break the "treat empty as docbuilder" rule that the spec
// relies on.
func TestNewDocument_DefaultsIncludeSourceKind(t *testing.T) {
	doc := NewDocument()
	require.Equal(t, SourceUnknown, doc.SourceKind,
		"NewDocument must default SourceKind to SourceUnknown so pre-SourceKind chunks serialize compatibly")
}

// TestNewChunk_DefaultsIncludeMetadataAndSourceKind pins the
// analogous contract for Chunk. Metadata is nil by default (not an
// empty map) so JSON / BSON omitempty drops the field on
// serialization. SourceKind defaults to SourceUnknown for the same
// reason as Document.
func TestNewChunk_DefaultsIncludeMetadataAndSourceKind(t *testing.T) {
	chunk := NewChunk()
	require.Nil(t, chunk.Metadata,
		"NewChunk must default Metadata to nil so omitempty drops the field for chunks without source-specific metadata")
	require.Equal(t, SourceUnknown, chunk.SourceKind,
		"NewChunk must default SourceKind to SourceUnknown for backwards compatibility")
}

// TestSearchResult_ZeroValueIsSourceUnknown is the same contract
// for SearchResult (which is JSON-only — populated at query time
// from the parent chunk's metadata, not persisted).
func TestSearchResult_ZeroValueIsSourceUnknown(t *testing.T) {
	var r SearchResult
	require.Equal(t, SourceUnknown, r.SourceKind,
		"zero-value SearchResult must have SourceKind == SourceUnknown so callers that build a result inline get the backwards-compatible default")
}

// TestChunk_MetadataRoundTrip pins the contract that arbitrary
// source-specific key/value pairs land on a chunk and round-trip
// through assignment. The gitlab writer will use this to surface
// `state` / `author_username` from issue JSON to retrieval-time
// filters; if the field type or tag drifts, those filters stop
// matching.
func TestChunk_MetadataRoundTrip(t *testing.T) {
	chunk := NewChunk()
	chunk.Metadata = map[string]string{
		"state":           "open",
		"author_username": "alice",
	}
	require.Equal(t, "open", chunk.Metadata["state"])
	require.Equal(t, "alice", chunk.Metadata["author_username"])
	require.Len(t, chunk.Metadata, 2)
}

// TestSourceKind_SourceIcon_ReturnsNonEmptyForKnownKinds pins
// that each known SourceKind has a non-empty icon marker. The
// citation panel and search-results template consume this so a
// future contributor who adds a new kind without giving it an
// icon surfaces as a failing test, not a silent "no icon" in
// the rendered UI.
func TestSourceKind_SourceIcon_ReturnsNonEmptyForKnownKinds(t *testing.T) {
	cases := []SourceKind{
		SourceDocbuilder,
		SourceGitLab,
	}
	for _, k := range cases {
		t.Run(string(k), func(t *testing.T) {
			icon := k.SourceIcon()
			require.NotEmpty(t, string(icon),
				"%q must produce a non-empty icon marker", k)
		})
	}
}

// TestSourceKind_SourceIcon_UnknownReturnsEmpty pins the no-op
// rule for SourceUnknown. Pre-SourceKind chunks deserialize to
// the zero value (SourceUnknown); the chat sources panel must
// not show a misleading "GitLab" or "Doc" badge for these. The
// empty marker renders as nothing in the template (the {{ if }}
// guard skips it), so legacy chunks stay byte-identical in the
// rendered output.
func TestSourceKind_SourceIcon_UnknownReturnsEmpty(t *testing.T) {
	require.Empty(t, string(SourceUnknown.SourceIcon()),
		"SourceUnknown must produce an empty icon — pre-SourceKind chunks must not show a kind badge")
}

// TestSourceKind_SourceIcon_ContainsKindMarker pins that each
// icon includes a recognizable token for its kind. The exact
// HTML is allowed to evolve (CSS classes, glyphs, etc.) but the
// kind name must appear somewhere — a11y / debugging use this to
// distinguish sources without parsing the rest of the rendered
// HTML. Anchoring on a substring means a future contributor can
// swap Unicode glyphs for SVG icons without breaking the test,
// as long as the kind name stays visible.
func TestSourceKind_SourceIcon_ContainsKindMarker(t *testing.T) {
	cases := []struct {
		kind   SourceKind
		marker string
	}{
		{SourceDocbuilder, "docbuilder"},
		{SourceGitLab, "gitlab"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			require.Contains(t, string(tc.kind.SourceIcon()), tc.marker,
				"%q icon must contain %q somewhere (a11y / debugging anchor)", tc.kind, tc.marker)
		})
	}
}

// TestSourceKind_SourceIcon_IsDeterministic pins that the helper
// is pure: same input -> same output. A future contributor who
// accidentally wires in a random nonce (e.g. for cache-busting)
// would surface here.
func TestSourceKind_SourceIcon_IsDeterministic(t *testing.T) {
	first := SourceGitLab.SourceIcon()
	for range 3 {
		require.Equal(t, string(first), string(SourceGitLab.SourceIcon()),
			"SourceIcon must be deterministic across calls")
	}
}
