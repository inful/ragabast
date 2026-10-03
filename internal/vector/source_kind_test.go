package vector

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestInferSourceKind pins the UID-prefix backfill contract. Chunks
// ingested before models.Chunk carried a SourceKind field have no
// "source_kind" metadata entry. The search path needs to assign a
// kind so the post-filter (and downstream citation dispatch) can
// branch correctly. The inference rule:
//
//   - if metadata has source_kind (new chunks), trust it verbatim
//   - else if uid starts with "gitlab:", treat as SourceGitLab
//   - else treat as SourceDocbuilder (the legacy single-source default)
//
// Pinning the rule here means a future contributor who changes the
// prefix or default sees the failure surface at test time.
func TestInferSourceKind(t *testing.T) {
	cases := []struct {
		name     string
		metaKind string
		uid      string
		want     models.SourceKind
	}{
		{name: "explicit gitlab wins over prefix", metaKind: "gitlab", uid: "docbuilder:something:42", want: models.SourceGitLab},
		{name: "explicit docbuilder wins over no prefix", metaKind: "docbuilder", uid: "my-doc", want: models.SourceDocbuilder},
		{name: "empty meta + gitlab prefix → gitlab", metaKind: "", uid: "gitlab:group/bar:42", want: models.SourceGitLab},
		{name: "empty meta + non-gitlab uid → docbuilder", metaKind: "", uid: "my-doc", want: models.SourceDocbuilder},
		{name: "empty meta + empty uid → docbuilder (legacy default)", metaKind: "", uid: "", want: models.SourceDocbuilder},
		{name: "unknown meta value ignored; falls through to prefix", metaKind: "redmine", uid: "gitlab:foo:1", want: models.SourceGitLab},
		{name: "unknown meta + no prefix falls to docbuilder", metaKind: "redmine", uid: "doc", want: models.SourceDocbuilder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferSourceKind(tc.metaKind, tc.uid)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestMetadataToChunk_BackfillsSourceKindFromUIDPrefix pins the
// contract that metadataToChunk runs inferSourceKind on read — so
// legacy chunks (no "source_kind" metadata entry) get a populated
// kind based on their UID prefix. Without this, the hybrid search
// path (which round-trips chunks via metadataToChunk before
// building SearchResults) would leave SourceKind empty for legacy
// chunks, and the source-kind post-filter would incorrectly drop
// them from docbuilder-only queries.
func TestMetadataToChunk_BackfillsSourceKindFromUIDPrefix(t *testing.T) {
	cases := []struct {
		name string
		uid  string
		want models.SourceKind
	}{
		{name: "legacy gitlab uid", uid: "gitlab:group/bar:42", want: models.SourceGitLab},
		{name: "legacy non-gitlab uid", uid: "my-doc", want: models.SourceDocbuilder},
		{name: "empty uid", uid: "", want: models.SourceDocbuilder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]string{
				"chunk_id": "abc",
				"uid":      tc.uid,
			}
			chunk := metadataToChunk(meta, "body")
			require.Equal(t, tc.want, chunk.SourceKind)
		})
	}
}
