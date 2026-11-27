package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragabast/internal/chunker"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service/querycache"
	"github.com/ragabast/internal/vector"
	"github.com/ragabast/internal/webfetch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// webMirrorRig is a service wired for the web-mirror tests: a real
// vector store, a stub embeddings server, and an injectable page
// fetcher (the real one refuses loopback addresses, which is where
// test servers live).
type webMirrorRig struct {
	svc      *Service
	fetched  []string
	fetchErr error
}

func newWebMirrorRig(t *testing.T, allowedHosts ...string) *webMirrorRig {
	t.Helper()

	const dim = 4
	embSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1,0,0,0]}],"model":"stub"}`))
	}))
	t.Cleanup(embSrv.Close)

	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.VectorDB.PersistenceDir = filepath.Join(tmp, "vectors")
	cfg.VectorDB.EmbeddingDimension = dim
	cfg.Ollama.BaseURL = embSrv.URL
	cfg.Ollama.ChatBaseURL = embSrv.URL
	cfg.WebIngest.AllowedHosts = allowedHosts

	db, err := vector.NewVectorDB("web-mirror-test", dim, cfg.VectorDB.PersistenceDir, "web-mirror-model")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	embeddings := vector.NewOpenAIEmbeddingClientWithOptions(embSrv.URL, "stub", "", 5*time.Second, dim, 1, "", "")
	vectorOps := vector.NewVectorOperations(db, embeddings)

	svc := &Service{
		config:    cfg,
		parser:    newParserForTest(),
		chunker:   chunker.NewChunker(cfg.Processing.MaxChunkSize, cfg.Processing.MinChunkSize, cfg.Processing.ChunkOverlap),
		vectorOps: vectorOps,
		cache:     querycache.New[[]models.SearchResult](4, time.Hour),
		now:       time.Now,
	}

	rig := &webMirrorRig{svc: svc}
	svc.fetchWebPage = func(_ context.Context, rawURL string, _ webfetch.Policy) (*webfetch.Result, error) {
		rig.fetched = append(rig.fetched, rawURL)
		if rig.fetchErr != nil {
			return nil, rig.fetchErr
		}
		return &webfetch.Result{
			Markdown: "# Semantic Versioning\n\nMAJOR.MINOR.PATCH\n",
			Title:    "Semantic Versioning",
		}, nil
	}
	return rig
}

// docWithSources builds a docbuilder document that requests the
// given pages.
func docWithSources(uid string, sources ...string) string {
	out := "---\nuid: " + uid + "\ntitle: Doc " + uid + "\nfingerprint: fp-" + uid + "\n"
	if len(sources) > 0 {
		out += "web_sources:\n"
		var outSb86 strings.Builder
		for _, src := range sources {
			outSb86.WriteString("  - " + src + "\n")
		}
		out += outSb86.String()
	}
	out += "---\n\n# Title\n\nBody text.\n"
	return out
}

// TestIngestDocument_MirrorsRequestedWebSource pins the happy
// path: a document that requests a page gets that page mirrored
// into the corpus as a document of its own.
func TestIngestDocument_MirrorsRequestedWebSource(t *testing.T) {
	rig := newWebMirrorRig(t, "example.com")

	_, err := rig.svc.IngestDocument(context.Background(), docWithSources("doc-1", "https://example.com/spec"))
	require.NoError(t, err)

	require.Len(t, rig.fetched, 1, "the requested page must be fetched")
	assert.Equal(t, "https://example.com/spec", rig.fetched[0])

	// The mirror is a document of its own.
	mirrors := rig.svc.WebMirrors()
	require.Len(t, mirrors, 1)
	assert.Equal(t, "https://example.com/spec", mirrors[0].SourceURL)
	assert.Equal(t, "web:example.com-spec", mirrors[0].UID)
	assert.Equal(t, []string{"doc-1"}, mirrors[0].DeclaredBy)

	doc, err := rig.svc.GetDocument(context.Background(), "web:example.com-spec")
	require.NoError(t, err, "the mirrored page must be in the corpus")
	require.NotEmpty(t, doc.Chunks)
	assert.Equal(t, models.SourceWeb, doc.Chunks[0].SourceKind,
		"a mirrored page is a 'web' document")
	assert.Contains(t, doc.Chunks[0].Content, "MAJOR.MINOR.PATCH")
	assert.Equal(t, "https://example.com/spec", doc.Chunks[0].DocumentURLs[0],
		"the citation points at the original page")
}

// TestIngestDocument_RefusesHostsOutsideTheAllowlist pins the
// security boundary: a document can only cause a fetch of a host
// the operator allowed. The document still ingests.
func TestIngestDocument_RefusesHostsOutsideTheAllowlist(t *testing.T) {
	rig := newWebMirrorRig(t, "example.com")

	_, err := rig.svc.IngestDocument(context.Background(),
		docWithSources("doc-1", "https://evil.testnet.example/spec"))
	require.NoError(t, err, "the document's own ingest must not fail")

	assert.Empty(t, rig.fetched, "a host outside the allowlist must not be fetched")

	mirrors := rig.svc.WebMirrors()
	require.Len(t, mirrors, 1, "the attempt is still recorded")
	assert.Contains(t, mirrors[0].Error, "not allowed")
}

// TestIngestDocument_NoAllowlistMeansNoFetching pins that the
// feature is off until the operator turns it on.
func TestIngestDocument_NoAllowlistMeansNoFetching(t *testing.T) {
	rig := newWebMirrorRig(t)

	_, err := rig.svc.IngestDocument(context.Background(), docWithSources("doc-1", "https://example.com/spec"))
	require.NoError(t, err)

	assert.Empty(t, rig.fetched, "no allowlist means no fetching")
}

// TestIngestDocument_FetchFailureDoesNotFailTheDocument pins that
// a page that cannot be fetched leaves the document's ingest
// untouched — the mirror is a convenience, not part of the
// document.
func TestIngestDocument_FetchFailureDoesNotFailTheDocument(t *testing.T) {
	rig := newWebMirrorRig(t, "example.com")
	rig.fetchErr = errors.New("connection refused")

	doc, err := rig.svc.IngestDocument(context.Background(), docWithSources("doc-1", "https://example.com/spec"))
	require.NoError(t, err, "a failed mirror must not fail the document")
	require.NotNil(t, doc)

	mirrors := rig.svc.WebMirrors()
	require.Len(t, mirrors, 1)
	assert.Contains(t, mirrors[0].Error, "connection refused")
}

// TestIngestDocument_DropsMirrorNobodyRequestsAnymore pins the
// garbage collection: when the document stops requesting a page,
// the mirror goes away.
func TestIngestDocument_DropsMirrorNobodyRequestsAnymore(t *testing.T) {
	rig := newWebMirrorRig(t, "example.com")
	ctx := context.Background()

	_, err := rig.svc.IngestDocument(ctx, docWithSources("doc-1", "https://example.com/spec"))
	require.NoError(t, err)
	require.Len(t, rig.svc.WebMirrors(), 1)

	// The same document, no longer requesting the page.
	_, err = rig.svc.IngestDocument(ctx, docWithSources("doc-1"))
	require.NoError(t, err)

	assert.Empty(t, rig.svc.WebMirrors(), "a mirror nobody requests must go away")
	_, err = rig.svc.GetDocument(ctx, "web:example.com-spec")
	assert.Error(t, err, "the mirror document must be gone from the corpus")
}

// TestIngestDocument_KeepsMirrorAnotherDocumentStillRequests pins
// that a mirror stays as long as ANY document requests it.
func TestIngestDocument_KeepsMirrorAnotherDocumentStillRequests(t *testing.T) {
	rig := newWebMirrorRig(t, "example.com")
	ctx := context.Background()

	_, err := rig.svc.IngestDocument(ctx, docWithSources("doc-1", "https://example.com/spec"))
	require.NoError(t, err)
	_, err = rig.svc.IngestDocument(ctx, docWithSources("doc-2", "https://example.com/spec"))
	require.NoError(t, err)

	// doc-1 stops requesting it; doc-2 still does.
	_, err = rig.svc.IngestDocument(ctx, docWithSources("doc-1"))
	require.NoError(t, err)

	mirrors := rig.svc.WebMirrors()
	require.Len(t, mirrors, 1, "doc-2 still wants the mirror")
	assert.Equal(t, []string{"doc-2"}, mirrors[0].DeclaredBy)
}

// TestWebMirrorUID_IsStableAndReadable pins the identity rule: the
// same URL always maps to the same UID, and the UID stays readable.
func TestWebMirrorUID_IsStableAndReadable(t *testing.T) {
	first := webMirrorUID("https://example.com/spec/v2.0.0.html")
	second := webMirrorUID("https://example.com/spec/v2.0.0.html")

	assert.Equal(t, first, second, "the same URL must map to the same UID")
	assert.Equal(t, "web:example.com-spec-v2.0.0.html", first)
	assert.NotEqual(t, first, webMirrorUID("https://example.com/other"),
		"different URLs must map to different UIDs")
}
