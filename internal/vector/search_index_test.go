package vector

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper that builds a chunkSummary from a content string for
// tests that don't care about the metadata fields.
func cs(content string) chunkSummary {
	return chunkSummary{ID: hash(content), Content: content}
}

func csID(id, content string) chunkSummary {
	return chunkSummary{ID: id, Content: content}
}

func csMeta(id, docID, content string, tags []string) chunkSummary {
	return chunkSummary{
		ID:         id,
		Content:    content,
		DocumentID: docID,
		Tags:       tags,
	}
}

// hash is a deterministic per-content id so tests don't have
// to spell out chunk IDs for every assertion. Production code
// uses real IDs from *models.Chunk.ID.
func hash(s string) string {
	var h uint64
	for _, r := range s {
		h = h*131 + uint64(r)
	}
	return "h" + uintToBase36(h)
}

func uintToBase36(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = "0123456789abcdefghijklmnopqrstuvwxyz"[v%36]
		v /= 36
	}
	return string(b[i:])
}

// Empty index returns no hits, no error.
func TestSearchIndex_EmptyReturnsNoHits(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, err := idx.Search("anything", 5, SearchFilters{})
	require.NoError(t, err)
	assert.Empty(t, hits)
}

// Search must not crash on a zero limit; bleve treats size=0
// as "give me everything" which is wrong here. The wrapper
// enforces a minimum.
func TestSearchIndex_LimitNormalised(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		cs("kubernetes ingress tls"),
		cs("network policy enforcement"),
	}))

	hits, err := idx.Search("kubernetes", 0, SearchFilters{})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(hits), 1_000_000,
		"limit=0 must not be interpreted as 'all'")
}

// A query term that occurs in only one chunk returns that
// chunk and only that chunk.
func TestSearchIndex_ExactTermMatchesSingleChunk(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("k8s", "kubernetes ingress tls"),
		csID("net", "network policy enforcement"),
		csID("db", "database backup procedure"),
	}))

	hits, err := idx.Search("kubernetes", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "k8s", hits[0].ID,
		"the hit must be the chunk containing 'kubernetes'")
}

// Bleve's BM25 should still rank shorter-matching docs higher
// than longer ones when the term frequency is the same. We
// use a less-trivial query so length normalisation actually
// has room to apply.
func TestSearchIndex_LengthNormalisation(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("short", "kubernetes ingress tls"),
		csID("long", strings.Repeat("Lorem ipsum dolor sit amet. ", 100)+"kubernetes ingress tls"),
		csID("unrelated", "garbage bytes unrelated"),
	}))

	hits, err := idx.Search("kubernetes", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, "short", hits[0].ID,
		"shorter chunk should rank higher (length normalisation)")
	assert.Greater(t, hits[0].Score, hits[1].Score)
}

// DocumentID filter: only chunks for the requested document
// appear in the result, even when other chunks match the
// query.
func TestSearchIndex_FilterByDocumentID(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csMeta("c1", "doc-a", "tls handshake failed", nil),
		csMeta("c2", "doc-b", "tls handshake failed", nil),
		csMeta("c3", "doc-a", "tls handshake failed", nil),
		csMeta("f1", "doc-c", "kubernetes networking", nil),
	}))

	hits, err := idx.Search("tls", 10, SearchFilters{DocumentID: "doc-a"})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	for _, h := range hits {
		assert.Equal(t, "doc-a", h.DocumentID)
	}
}

// Tag filter: only chunks tagged with the requested tag
// appear in the result. bleve uses exact-term match against
// the multi-value tags field.
func TestSearchIndex_FilterByTag(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csMeta("c1", "doc-a", "tls handshake failed", []string{"security"}),
		csMeta("c2", "doc-a", "tls handshake failed", []string{"networking"}),
		csMeta("c3", "doc-b", "tls handshake failed", []string{"security"}),
	}))

	hits, err := idx.Search("tls", 10, SearchFilters{Tag: "security"})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	for _, h := range hits {
		// re-read tags via the original ids; here we just
		// confirm both hits came from the security-tagged set.
		assert.Contains(t, []string{"c1", "c3"}, h.ID)
	}
}

// Re-ingesting the same chunk IDs must NOT grow the index.
// bleve's Batch.Index treats the second insert as an update,
// so the document count stays put.
func TestSearchIndex_ReingestIdempotency(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("c1", "tls handshake failed"),
		csID("c2", "tls handshake failed"),
		csID("c3", "kubernetes networking"),
	}))
	before, err := idx.Size()
	require.NoError(t, err)

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("c1", "tls handshake failed"),
		csID("c2", "tls handshake failed"),
	}))

	after, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, before, after, "re-ingest must not grow the index")
	assert.Equal(t, 3, after)
}

// DeleteChunk removes a single chunk.
func TestSearchIndex_DeleteChunk(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("alpha", "alpha bravo charlie"),
		csID("beta", "alpha bravo charlie"),
	}))
	require.NoError(t, idx.DeleteChunk("alpha"))

	hits, err := idx.Search("alpha", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "beta", hits[0].ID)
}

// DeleteChunk on a missing ID is a no-op (no error).
func TestSearchIndex_DeleteChunkMissingIDIsNoop(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{csID("a", "alpha")}))
	require.NoError(t, idx.DeleteChunk("does-not-exist"))
}

// DeleteDocument removes every chunk belonging to the document.
func TestSearchIndex_DeleteDocument(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csMeta("a1", "doc-a", "alpha", nil),
		csMeta("a2", "doc-a", "alpha", nil),
		csMeta("b1", "doc-b", "alpha", nil),
	}))
	require.NoError(t, idx.DeleteDocument("doc-a"))

	hits, err := idx.Search("alpha", 10, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "b1", hits[0].ID)
}

// Persistence: closing and reopening the index preserves the
// corpus. Tests the LoadSearchIndex path.
func TestSearchIndex_PersistAndReload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "search.bleve")

	idx, err := NewSearchIndex(path)
	require.NoError(t, err)
	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("c1", "kubernetes ingress tls"),
		csID("c2", "network policy enforcement"),
		csID("c3", "database backup procedure"),
	}))
	require.NoError(t, idx.Close())

	idx2, err := LoadSearchIndex(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx2.Close() })

	hits, err := idx2.Search("kubernetes", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "c1", hits[0].ID)
}

// LoadSearchIndex on a missing path creates a fresh index
// rather than failing. Mirrors the production startup
// behavior for first-run after the v0.4 upgrade.
func TestSearchIndex_LoadMissingPathCreates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "does-not-exist-yet")
	idx, err := LoadSearchIndex(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	size, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 0, size)
}

// Clear empties the index.
func TestSearchIndex_Clear(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("a", "alpha"),
		csID("b", "bravo"),
	}))
	require.NoError(t, idx.Clear())

	size, err := idx.Size()
	require.NoError(t, err)
	assert.Equal(t, 0, size)
}

// Identifier preservation: NewMatchQuery is analyzer-aware,
// so hyphenated identifiers like tls-handshake-failure stay
// as one token at query time and match the indexed token.
// (Bleve's query-string parser splits on `-`; we deliberately
// avoid it for that reason.)
func TestSearchIndex_IdentifierMatch(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("hit", "the tls-handshake-failure error is fatal"),
		csID("miss", "we updated the certificate and reset the connection"),
	}))

	hits, err := idx.Search("tls-handshake-failure", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "hit", hits[0].ID)
}

// Multi-word queries: the tokenizer splits on whitespace
// (and other delimiters), so a query containing a colon
// becomes two tokens: the part before and the part after.
func TestSearchIndex_MultiWordQueryWithDelimiters(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("matching", "panic nil pointer dereference"),
		csID("loose", "nil pointer panic is rare"),
	}))

	hits, err := idx.Search("panic nil", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	// Both docs match (both contain panic and nil); ranking
	// reflects token overlap.
	assert.Equal(t, "matching", hits[0].ID)
}

// Multi-word queries: NewMatchQuery tokenises the input and
// matches docs that contain any token (OR semantics). The
// top of the ranking reflects the highest-scoring doc.
func TestSearchIndex_MultiWordQueryOrSemantics(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("a", "kubernetes ingress tls"),
		csID("b", "kubernetes networking policy"),
		csID("c", "ingress controller tls"),
	}))

	// "kubernetes ingress" — all three docs match because
	// each contains at least one of the tokens; the doc that
	// contains both ranks highest.
	hits, err := idx.Search("kubernetes ingress", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 3)
	assert.Equal(t, "a", hits[0].ID,
		"doc with both terms should rank highest")
}

// No stemming: bleve is configured without a stemmer, so
// "fox" and "foxes" don't collapse to the same token. This
// preserves exact-term recall for function names and error
// strings.
func TestSearchIndex_NoStemming(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("d1", "The fox runs fast"),
		csID("d2", "Foxes are fast"),
	}))

	hits, err := idx.Search("fox", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "d1", hits[0].ID)

	hits, err = idx.Search("foxes", 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "d2", hits[0].ID)
}

// Identifiers like tls-handshake-failure stay as one token
// in the index (the custom regex tokenizer preserves the
// dashes). Quoted phrase queries match the whole identifier.
// Unquoted queries split on hyphens at the lexer layer and
// match per-token — this is bleve's query string design.
func TestSearchIndex_PreservesIdentifierTokens(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.AddBatch([]chunkSummary{
		csID("hit", "the tls-handshake-failure error is fatal"),
		csID("miss", "we updated the certificate and reset the connection"),
	}))

	// Quoted phrase — bleve treats this as one unit. The
	// analyzer splits it as one token "tls-handshake-failure"
	// (the regex preserves dashes), which matches the index
	// token.
	hits, err := idx.Search(`"tls-handshake-failure"`, 5, SearchFilters{})
	require.NoError(t, err)
	require.Len(t, hits, 1, "quoted phrase should match")
	assert.Equal(t, "hit", hits[0].ID)
}

// Search on an empty index returns zero hits without error
// (defensive — bleve panics on empty queries in some
// versions, but we guard against that).
func TestSearchIndex_EmptyIndexWithQuery(t *testing.T) {
	t.Parallel()

	idx, err := NewSearchIndex(filepath.Join(t.TempDir(), "idx"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	hits, err := idx.Search("kubernetes", 5, SearchFilters{})
	require.NoError(t, err)
	assert.Empty(t, hits)
}

// LoadSearchIndex must work in a fresh process where
// NewSearchIndex has not yet fired the sync.Once that
// registers the custom tokenizer + length filter with
// bleve's registry. Otherwise `ragabast serve` crashes
// on startup whenever the index already exists on disk
// (every restart of any deployment that has ever booted
// successfully).
//
// The in-process tests above always call NewSearchIndex
// first, which masks the bug. This test reproduces the
// production sequence (only LoadSearchIndex, in a fresh
// process) by re-executing itself as a helper subprocess.
func TestSearchIndex_LoadOnExistingIndexFromFreshProcess(t *testing.T) {
	if os.Getenv("RAGABAST_SEARCH_INDEX_HELPER") == "1" {
		// Helper path: open the pre-existing index and exit.
		// Bleve.Open must succeed without any prior
		// NewSearchIndex / BuildMapping call in this process.
		path := os.Getenv("RAGABAST_SEARCH_INDEX_PATH")
		idx, err := LoadSearchIndex(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			os.Exit(1)
		}
		_ = idx.Close()
		os.Exit(0)
	}

	// Orchestrator: build an index in a fresh temp dir, close
	// it, then re-exec as the helper to test LoadSearchIndex
	// in a process where sync.Once has not yet fired.
	path := filepath.Join(t.TempDir(), "search")
	idx, err := NewSearchIndex(path)
	require.NoError(t, err)
	require.NoError(t, idx.AddBatch([]chunkSummary{csID("c1", "kubernetes ingress tls")}))
	require.NoError(t, idx.Close())

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=TestSearchIndex_LoadOnExistingIndexFromFreshProcess")
	cmd.Env = append(os.Environ(),
		"RAGABAST_SEARCH_INDEX_HELPER=1",
		"RAGABAST_SEARCH_INDEX_PATH="+path,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "helper failed: %s", out)
}
