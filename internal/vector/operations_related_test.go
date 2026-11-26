package vector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relatedEmbeddings returns an embeddings client whose vectors
// depend on the CONTENT, not on the batch position: a text that
// mentions "alpha" lands on the alpha axis, "beta" on the beta
// axis, and so on. That makes "these two documents are about the
// same thing" expressible in a test.
func relatedEmbeddings(t *testing.T) *OpenAIEmbeddingClient {
	t.Helper()

	const dim = 4
	axes := []string{"alpha", "beta", "gamma", "delta"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)

		var req struct {
			Input []string `json:"input"`
		}
		assert.NoError(t, json.Unmarshal(body, &req))

		data := make([]map[string]any, 0, len(req.Input))
		for i, input := range req.Input {
			vec := make([]float32, dim)
			for axis, name := range axes {
				if strings.Contains(strings.ToLower(input), name) {
					vec[axis] = 1
				}
			}
			data = append(data, map[string]any{
				"object":    "embedding",
				"index":     i,
				"embedding": vec,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   data,
			"model":  "related-stub",
		})
	}))
	t.Cleanup(srv.Close)

	return NewOpenAIEmbeddingClientWithOptions(srv.URL, "related-stub", "", 5*time.Second, 0, 1, "", "")
}

// relatedTestDoc builds a document whose ID and UID are the same,
// the way the docbuilder parser produces them.
func relatedTestDoc(id string, chunks ...string) *models.Document {
	doc := &models.Document{
		ID:          id,
		UID:         id,
		Title:       "Doc " + id,
		Fingerprint: id + "-fp",
		Content:     chunks[0],
		Tags:        []string{"t1"},
		Categories:  []string{"c1"},
	}
	for i, content := range chunks {
		doc.Chunks = append(doc.Chunks, models.Chunk{
			ID:                 id + "-c" + string(rune('0'+i)),
			DocumentID:         id,
			Content:            content,
			DocumentTitle:      "Doc " + id,
			UID:                id,
			Fingerprint:        id + "-fp",
			DocumentTags:       []string{"t1"},
			DocumentCategories: []string{"c1"},
		})
	}
	return doc
}

// newRelatedOps wires a vector layer with the content-aware
// embeddings client.
func newRelatedOps(t *testing.T) *VectorOperations {
	t.Helper()

	db, err := NewVectorDB("related-"+t.Name(), 4, "", "related-model")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	vo := NewVectorOperations(db, relatedEmbeddings(t))
	return vo
}

// TestRelatedDocuments_ReturnsNearestDocuments pins the contract:
// the documents whose content is closest to the queried one, most
// similar first, with the queried document excluded.
func TestRelatedDocuments_ReturnsNearestDocuments(t *testing.T) {
	vo := newRelatedOps(t)
	ctx := context.Background()

	require.NoError(t, vo.IngestDocument(ctx, relatedTestDoc("doc-a", "alpha ")))
	require.NoError(t, vo.IngestDocument(ctx, relatedTestDoc("doc-b", "alpha")))
	require.NoError(t, vo.IngestDocument(ctx, relatedTestDoc("doc-c", "beta")))

	related, err := vo.RelatedDocuments(ctx, "doc-a", 5)
	require.NoError(t, err)
	require.Len(t, related, 2, "only the other two documents qualify")

	assert.Equal(t, "doc-b", related[0].UID, "the alpha document must rank first")
	assert.Equal(t, "doc-c", related[1].UID)
	assert.Greater(t, related[0].Score, related[1].Score, "scores must be ordered")

	for _, doc := range related {
		assert.NotEqual(t, "doc-a", doc.UID, "the queried document must not be in the answer")
	}
}

// TestRelatedDocuments_RespectsLimit pins the limit: the caller
// asks for fewer documents than exist, and gets that many.
func TestRelatedDocuments_RespectsLimit(t *testing.T) {
	vo := newRelatedOps(t)
	ctx := context.Background()

	require.NoError(t, vo.IngestDocument(ctx, relatedTestDoc("doc-a", "alpha")))
	require.NoError(t, vo.IngestDocument(ctx, relatedTestDoc("doc-b", "alpha")))
	require.NoError(t, vo.IngestDocument(ctx, relatedTestDoc("doc-c", "alpha")))

	related, err := vo.RelatedDocuments(ctx, "doc-a", 1)
	require.NoError(t, err)
	assert.Len(t, related, 1)
}

// TestRelatedDocuments_UnknownUID pins the "no such document"
// signal: a UID that was never ingested is ErrNotFound, not an
// empty list — the HTTP layer turns it into a 404.
func TestRelatedDocuments_UnknownUID(t *testing.T) {
	vo := newRelatedOps(t)

	_, err := vo.RelatedDocuments(context.Background(), "nope", 5)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestRelatedDocuments_GroupsChunksByDocument pins that a
// document's score is its BEST chunk hit, and that a document with
// many chunks is still listed once.
func TestRelatedDocuments_GroupsChunksByDocument(t *testing.T) {
	vo := newRelatedOps(t)
	ctx := context.Background()

	require.NoError(t, vo.IngestDocument(ctx, relatedTestDoc("doc-a", "alpha")))
	require.NoError(t, vo.IngestDocument(ctx, relatedTestDoc("doc-b", "alpha", "alpha", "alpha")))

	related, err := vo.RelatedDocuments(ctx, "doc-a", 5)
	require.NoError(t, err)
	require.Len(t, related, 1, "a document with three chunks is still one entry")
	assert.Equal(t, "doc-b", related[0].UID)
}
