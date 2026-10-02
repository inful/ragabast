package service

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragabast/internal/chunker"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/parser"
	"github.com/ragabast/internal/service/querycache"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/require"
)

// newParserForTest returns a real *parser.DocbuilderParser. The
// service-layer tests need a real parser because the preflight
// rules depend on the parser populating Draft / Date / PublishDate
// / ExpiryDate on the parsed Document.
func newParserForTest() *parser.DocbuilderParser {
	return parser.NewDocbuilderParser()
}

// unpublishedTestRig is the same shape as fingerprintServiceTestRig
// but exposes both the *Service and the underlying *vector.VectorDB
// so tests can assert on the embeddings store directly. The service
// uses SetNow to pin "now" to a fixed time so the date-relative
// preflight rules are deterministic.
type unpublishedTestRig struct {
	svc *Service
	db  *vector.VectorDB
}

// newUnpublishedTestRig constructs the rig in a temp dir and pins
// the service clock to 2026-06-15 12:00:00 UTC. Returns the rig and
// registers a cleanup that closes the embedding server and removes
// the temp dir.
func newUnpublishedTestRig(t *testing.T) *unpublishedTestRig {
	t.Helper()

	const dim = 4
	embSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1,0,0,0]}],"model":"stub"}`))
	}))
	t.Cleanup(embSrv.Close)

	tmp := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.VectorDB.EmbeddingDimension = dim
	cfg.VectorDB.PersistenceDir = tmp

	db, err := vector.NewVectorDB("test", dim, tmp, "stub")
	require.NoError(t, err)

	embeddings := vector.NewOpenAIEmbeddingClientWithOptions(
		embSrv.URL, "stub", "", 5*time.Second, 0, 1, "", "",
	)
	vectorOps := vector.NewVectorOperations(db, embeddings)

	svc := &Service{
		config:    cfg,
		parser:    newParserForTest(),
		chunker:   chunker.NewChunker(cfg.Processing.MaxChunkSize, cfg.Processing.MinChunkSize, cfg.Processing.ChunkOverlap),
		vectorOps: vectorOps,
		// Wire a real cache so the cache-invalidation test
		// has a real target. querycache.New with capacity=0
		// would disable the cache (every Get re-runs the
		// loader), which would mask the bug we're pinning.
		cache: querycache.New[[]models.SearchResult](16, time.Hour),
		now: func() time.Time {
			return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
		},
	}
	return &unpublishedTestRig{svc: svc, db: db}
}

// hasDocument returns true iff a document with the given UID is
// present in the embeddings store.
func (r *unpublishedTestRig) hasDocument(t *testing.T, uid string) bool {
	t.Helper()
	docs, err := r.db.GetUniqueDocuments(context.Background())
	if err != nil {
		t.Fatalf("get unique documents: %v", err)
	}
	for _, d := range docs {
		if d.UID == uid {
			return true
		}
	}
	return false
}

// captureLog swaps log's writer for a buffer for the duration of
// the test body, then restores. Used to assert on the preflight
// log lines.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	oldOut := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	})
	return buf
}

// TestIngestDocument_NoFlags_DocumentIsEmbedded is the
// regression guard. A document with no Hugo "don't publish"
// markers must ingest normally. Without this test, a typo in
// the preflight (e.g. returning filtered=true by mistake)
// would silently lose every ingest.
func TestIngestDocument_NoFlags_DocumentIsEmbedded(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	doc, err := rig.svc.IngestDocument(context.Background(), publishableDoc())
	require.NoError(t, err)
	require.NotNil(t, doc)
	require.True(t, rig.hasDocument(t, "publishable"),
		"a doc with no Hugo markers must be embedded normally")
}

// TestIngestDocument_DraftTrue_NewDocIsIgnored pins the cold-start
// path from issue #97: an unpublished doc that was never embedded
// is silently ignored. No error surfaces, no chunkAndIngest call,
// and the doc is NOT in the store afterwards.
func TestIngestDocument_DraftTrue_NewDocIsIgnored(t *testing.T) {
	rig := newUnpublishedTestRig(t)
	buf := captureLog(t)

	doc, err := rig.svc.IngestDocument(context.Background(), draftDoc("unseen"))
	require.NoError(t, err, "an unpublished doc that was never embedded is not an error")
	require.NotNil(t, doc)
	require.False(t, rig.hasDocument(t, "unseen"),
		"the unembedded draft doc must not be added to the store")
	require.Contains(t, buf.String(), "skipping ingest of unpublished doc",
		"the preflight must log a skip line")
}

// TestIngestDocument_DraftTrue_ExistingDocIsRemoved pins the
// headline issue path. Ingest a publishable doc, verify it's
// there, then re-ingest the same UID with draft: true. The
// preflight must remove the previously-embedded copy.
//
// This is the delete-on-ingest contract: the moment a doc turns
// into a draft, the database stops holding it.
func TestIngestDocument_DraftTrue_ExistingDocIsRemoved(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	// First, ingest a publishable doc.
	_, err := rig.svc.IngestDocument(context.Background(), publishableDoc())
	require.NoError(t, err)
	require.True(t, rig.hasDocument(t, "publishable"),
		"sanity check: the publishable doc must be embedded first")

	// Now re-ingest with draft: true. Same UID.
	buf := captureLog(t)
	doc, err := rig.svc.IngestDocument(context.Background(), draftDoc("publishable"))
	require.NoError(t, err)
	require.NotNil(t, doc)
	require.False(t, rig.hasDocument(t, "publishable"),
		"the previously-embedded doc must be removed when the new copy is a draft")
	require.Contains(t, buf.String(), "removed unpublished doc",
		"the preflight must log a removal line")
}

// TestIngestDocument_DateInFuture_ExistingDocIsRemoved pins the
// `date: <future>` rule. Operators who set a future date after
// publishing must see the doc disappear from the embeddings on
// the next re-ingest.
func TestIngestDocument_DateInFuture_ExistingDocIsRemoved(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	_, err := rig.svc.IngestDocument(context.Background(), publishableDoc())
	require.NoError(t, err)
	require.True(t, rig.hasDocument(t, "publishable"))

	future := rig.svc.now().Add(24 * time.Hour).Format(time.RFC3339)
	raw := "---\nuid: publishable\ndate: " + future + "\n---\n\nbody\n"
	_, err = rig.svc.IngestDocument(context.Background(), raw)
	require.NoError(t, err)
	require.False(t, rig.hasDocument(t, "publishable"),
		"a doc with date: <future> must be removed on re-ingest")
}

// TestIngestDocument_PublishDateInFuture_NewDocIsIgnored pins the
// `publishDate: <future>` rule on the cold-start path.
func TestIngestDocument_PublishDateInFuture_NewDocIsIgnored(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	future := rig.svc.now().Add(24 * time.Hour).Format(time.RFC3339)
	raw := "---\nuid: future-publish\npublishDate: " + future + "\n---\n\nbody\n"

	doc, err := rig.svc.IngestDocument(context.Background(), raw)
	require.NoError(t, err)
	require.NotNil(t, doc)
	require.False(t, rig.hasDocument(t, "future-publish"),
		"a never-embedded doc with publishDate in the future must be ignored")
}

// TestIngestDocument_ExpiryDateInPast_ExistingDocIsRemoved pins
// the `expiryDate: <past>` rule on the existing-doc path. This is
// the "take down this post" scenario.
func TestIngestDocument_ExpiryDateInPast_ExistingDocIsRemoved(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	_, err := rig.svc.IngestDocument(context.Background(), publishableDoc())
	require.NoError(t, err)
	require.True(t, rig.hasDocument(t, "publishable"))

	past := rig.svc.now().Add(-24 * time.Hour).Format(time.RFC3339)
	raw := "---\nuid: publishable\nexpiryDate: " + past + "\n---\n\nbody\n"
	_, err = rig.svc.IngestDocument(context.Background(), raw)
	require.NoError(t, err)
	require.False(t, rig.hasDocument(t, "publishable"),
		"a doc with expiryDate in the past must be removed on re-ingest")
}

// TestIngestDocument_AllFourMarkers_Combined pins that all four
// Hugo markers can be present and any one of them filtering still
// removes the doc. We assert only on the headline case (all four
// set) for brevity; the per-marker tests above cover each rule.
func TestIngestDocument_AllFourMarkers_Combined(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	// First, ingest a publishable doc with the same UID.
	_, err := rig.svc.IngestDocument(context.Background(), publishableDoc())
	require.NoError(t, err)
	require.True(t, rig.hasDocument(t, "publishable"))

	now := rig.svc.now()
	future := now.Add(24 * time.Hour).Format(time.RFC3339)
	past := now.Add(-24 * time.Hour).Format(time.RFC3339)
	raw := strings.Join([]string{
		"---",
		"uid: publishable",
		"draft: true",
		"date: " + future,
		"publishDate: " + future,
		"expiryDate: " + past,
		"---",
		"",
		"body",
		"",
	}, "\n")
	_, err = rig.svc.IngestDocument(context.Background(), raw)
	require.NoError(t, err)
	require.False(t, rig.hasDocument(t, "publishable"),
		"any single Hugo marker firing must be enough to filter the doc")
}

// TestIngestDocument_ReingestAfterFilter_Restores pins the
// symmetric case: filter → remove, then publish again → re-add.
// This is the contract an operator relies on when they flip a
// doc back and forth between draft and published.
func TestIngestDocument_ReingestAfterFilter_Restores(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	// Step 1: publishable doc lands in the store.
	_, err := rig.svc.IngestDocument(context.Background(), publishableDoc())
	require.NoError(t, err)
	require.True(t, rig.hasDocument(t, "publishable"))

	// Step 2: re-ingest as a draft. Doc is removed.
	_, err = rig.svc.IngestDocument(context.Background(), draftDoc("publishable"))
	require.NoError(t, err)
	require.False(t, rig.hasDocument(t, "publishable"))

	// Step 3: re-ingest as publishable again. Doc returns.
	_, err = rig.svc.IngestDocument(context.Background(), publishableDoc())
	require.NoError(t, err)
	require.True(t, rig.hasDocument(t, "publishable"),
		"a doc that flips back from draft to publishable must be re-embedded")
}

// TestIngestFile_Draft_IgnoredIsNotAnError pins the same contract
// on the filesystem ingest path. CLI directory ingest must
// silently skip draft files; per-file failure counts must NOT
// include them.
func TestIngestFile_Draft_IgnoredIsNotAnError(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	dir := t.TempDir()
	draftPath := dir + "/draft.md"
	publishablePath := dir + "/publishable.md"
	writeFile(t, draftPath, draftDoc("draft-file"))
	writeFile(t, publishablePath, publishableDocWithUID("publishable-file"))

	buf := captureLog(t)

	result, err := rig.svc.IngestDirectory(context.Background(), dir)
	require.NoError(t, err)
	require.Equal(t, 2, result.Processed,
		"both files must count as processed (draft is a successful skip)")
	require.Equal(t, 0, result.Failed)
	require.Empty(t, result.Errors)

	require.False(t, rig.hasDocument(t, "draft-file"),
		"the draft file must not be embedded")
	require.True(t, rig.hasDocument(t, "publishable-file"),
		"the publishable file must be embedded")
	require.Contains(t, buf.String(), "skipping ingest of unpublished doc",
		"the preflight must log a skip line")
}

// TestIngestDocument_DraftCacheInvalidated pins the cache
// invalidation contract: when the preflight deletes a doc, the
// query cache must be cleared so subsequent searches stop
// returning the now-deleted doc. Same policy as DeleteDocument.
//
// The cache only has a loader-shaped API (no Set/Has), so we plant
// an entry by calling Get with a loader and verifying that a
// follow-up call hits the cache (loader not called). After the
// preflight delete runs, the same follow-up call must invoke the
// loader again — proving the cache was cleared.
func TestIngestDocument_DraftCacheInvalidated(t *testing.T) {
	rig := newUnpublishedTestRig(t)

	// Ingest a publishable doc first.
	_, err := rig.svc.IngestDocument(context.Background(), publishableDoc())
	require.NoError(t, err)
	require.True(t, rig.hasDocument(t, "publishable"))

	// Plant a cache entry by calling Get once with a loader
	// that records "I was called".
	loaderCalled := 0
	load := func(_ context.Context) ([]models.SearchResult, error) {
		loaderCalled++
		return nil, nil
	}
	_, _, err = rig.svc.cache.Get(context.Background(), "test-key", load)
	require.NoError(t, err)
	require.Equal(t, 1, loaderCalled, "first Get must invoke the loader")

	// Sanity: a follow-up Get with the same key hits the
	// cache and does NOT re-invoke the loader.
	_, _, err = rig.svc.cache.Get(context.Background(), "test-key", load)
	require.NoError(t, err)
	require.Equal(t, 1, loaderCalled, "second Get must hit the cache, not the loader")

	// Run the preflight: re-ingest with draft: true. The
	// preflight deletes, which must call cache.Clear().
	_, err = rig.svc.IngestDocument(context.Background(), draftDoc("publishable"))
	require.NoError(t, err)

	// After the preflight, the cache must be empty: the same
	// Get must invoke the loader again because the prior
	// entry was cleared.
	_, _, err = rig.svc.cache.Get(context.Background(), "test-key", load)
	require.NoError(t, err)
	require.Equal(t, 2, loaderCalled,
		"after the preflight delete, the cache must be cleared and the loader must run again")
}

// publishableDoc is a minimal but well-formed docbuilder document
// with no Hugo "don't publish" markers.
func publishableDoc() string {
	return "---\nuid: publishable\nfingerprint: pub-fp\n---\n\nbody\n"
}

// publishableDocWithUID is publishableDoc but with an explicit UID.
// The two helpers stay distinct: tests that care about a specific
// UID parameterize the helper, tests that don't pin the default.
func publishableDocWithUID(uid string) string {
	return "---\nuid: " + uid + "\nfingerprint: " + uid + "-fp\n---\n\nbody\n"
}

// draftDoc is a docbuilder document with `draft: true` for the
// given UID.
func draftDoc(uid string) string {
	return "---\nuid: " + uid + "\nfingerprint: " + uid + "-fp\ndraft: true\n---\n\nbody\n"
}

// writeFile is a thin wrapper over os.WriteFile that surfaces any
// error as a test failure rather than letting it propagate.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
