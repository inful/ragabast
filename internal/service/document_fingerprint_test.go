package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/require"
)

// stubEmbeddingServer returns a real httptest.Server that
// answers OpenAI-style /v1/embeddings with a fixed 4-dim
// vector. Tests don't read the embedding values — they just
// need the request to succeed so the chromem-go ingest
// path completes. Hard-coded dim keeps the stub simple.
func stubEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1,0,0,0]}],"model":"stub"}`))
	}))
}

// fingerprintServiceTestRig builds a real *Service backed
// by an in-memory chromem-go collection. Same shape the
// rest of the service tests use (query_enrich_test.go,
// query_cache_integration_test.go) — slower than a hand-
// rolled stub but exercises the real vector code path so we
// catch regressions in the vector layer's DocumentFingerprint
// method, not just the service-layer plumbing.
type fingerprintServiceTestRig struct {
	svc *Service
	db  *vector.VectorDB
}

// newFingerprintServiceTestRig constructs the rig in a temp
// dir. Returns the rig and registers a cleanup that closes
// the embedding server and removes the temp dir.
func newFingerprintServiceTestRig(t *testing.T) *fingerprintServiceTestRig {
	t.Helper()

	const dim = 4
	embSrv := stubEmbeddingServer(t)
	t.Cleanup(embSrv.Close)

	tmp := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.VectorDB.EmbeddingDimension = dim
	cfg.VectorDB.PersistenceDir = tmp

	db, err := vector.NewVectorDB("test", dim, tmp, "stub")
	require.NoError(t, err)

	embeddings := vector.NewOpenAIEmbeddingClientWithOptions(
		embSrv.URL,
		"stub",
		"",
		5*time.Second,
		0,
		1,
		"",
		"",
	)
	vectorOps := vector.NewVectorOperations(db, embeddings)

	return &fingerprintServiceTestRig{
		svc: &Service{
			config:    cfg,
			vectorOps: vectorOps,
		},
		db: db,
	}
}

// ingestOneChunk stores a single chunk with the given UID and
// fingerprint so the fingerprint lookup has something to find.
// Returns the chunk's document_id (== UID, since the parser
// pins doc.ID = doc.UID).
func (r *fingerprintServiceTestRig) ingestOneChunk(t *testing.T, uid, fingerprint string) {
	t.Helper()
	chunk := &models.Chunk{
		ID:            "c-" + uid,
		DocumentID:    uid,
		DocumentTitle: "Doc " + uid,
		UID:           uid,
		HeaderPath:    "H1",
		Level:         1,
		Content:       "Body of " + uid,
		Fingerprint:   fingerprint,
	}
	vec := []float32{1.0, 0.0, 0.0, 0.0}
	require.NoError(t, r.db.AddChunk(context.Background(), chunk, vec))
}

// TestGetDocumentFingerprint_NotYetIngested pins the canonical
// "no document with this UID" response: exists=false, no error.
// The HTTP handler maps this to 404. False-positive exists
// (returning exists=true when no chunks match) is the failure
// mode that would mislead ingest pipelines into skipping
// documents they should be re-sending.
func TestGetDocumentFingerprint_NotYetIngested(t *testing.T) {
	rig := newFingerprintServiceTestRig(t)

	info, exists, err := rig.svc.GetDocumentFingerprint(context.Background(), "never-ingested")
	require.NoError(t, err)
	require.False(t, exists, "an UID that was never stored must report exists=false")
	require.Empty(t, info.Fingerprint, "the zero DocumentFingerprintInfo must have an empty fingerprint")
}

// TestGetDocumentFingerprint_ReturnsStoredValue pins the happy
// path: after ingesting a document, the lookup returns the
// fingerprint we stored. This is the core of the preflight
// contract — without it, ingest pipelines can't decide
// whether to skip or re-send.
func TestGetDocumentFingerprint_ReturnsStoredValue(t *testing.T) {
	rig := newFingerprintServiceTestRig(t)
	rig.ingestOneChunk(t, "adr-001", "sha256:abc123")

	info, exists, err := rig.svc.GetDocumentFingerprint(context.Background(), "adr-001")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, "sha256:abc123", info.Fingerprint)
}

// TestGetDocumentFingerprint_DifferentUIDsReturnDifferentValues
// pins the per-UID isolation: the vector layer must filter
// by document_id so two docs with different UIDs each return
// their own fingerprint. A regression that returned the same
// fingerprint for any UID would silently corrupt the
// preflight decision.
func TestGetDocumentFingerprint_DifferentUIDsReturnDifferentValues(t *testing.T) {
	rig := newFingerprintServiceTestRig(t)
	rig.ingestOneChunk(t, "adr-001", "sha256:abc")
	rig.ingestOneChunk(t, "adr-002", "sha256:xyz")

	a, exists, err := rig.svc.GetDocumentFingerprint(context.Background(), "adr-001")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, "sha256:abc", a.Fingerprint)

	b, exists, err := rig.svc.GetDocumentFingerprint(context.Background(), "adr-002")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, "sha256:xyz", b.Fingerprint)
}

// TestGetDocumentFingerprint_EmptyUID pins the input-validation
// contract: empty / whitespace-only UIDs return
// (zero, false, nil) WITHOUT doing a vector lookup. This is a
// service-layer optimization — don't burn a chromem query on
// garbage input.
func TestGetDocumentFingerprint_EmptyUID(t *testing.T) {
	rig := newFingerprintServiceTestRig(t)
	// Ingest one chunk so the vector layer has data — but the
	// empty UID lookup should still short-circuit before
	// reaching it.
	rig.ingestOneChunk(t, "real-uid", "sha256:real")

	cases := []string{"", " ", "\t", "\n", "   "}
	for _, uid := range cases {
		t.Run("uid="+uid, func(t *testing.T) {
			info, exists, err := rig.svc.GetDocumentFingerprint(context.Background(), uid)
			require.NoError(t, err)
			require.False(t, exists, "empty UID must not report as ingested")
			require.Empty(t, info.Fingerprint)
		})
	}
}

// TestGetDocumentFingerprint_TrimsUIDBeforeLookup pins the
// "tolerate leading/trailing whitespace" contract: the
// docbuilder parser trims frontmatter fields at ingest time,
// so a stored UID has no leading/trailing whitespace. A
// caller asking for " adr-001 " should still find the doc.
func TestGetDocumentFingerprint_TrimsUIDBeforeLookup(t *testing.T) {
	rig := newFingerprintServiceTestRig(t)
	rig.ingestOneChunk(t, "adr-001", "sha256:abc")

	info, exists, err := rig.svc.GetDocumentFingerprint(context.Background(), "  adr-001  ")
	require.NoError(t, err)
	require.True(t, exists,
		"UID with leading/trailing whitespace must still match the stored value")
	require.Equal(t, "sha256:abc", info.Fingerprint)
}
