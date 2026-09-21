package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// makeDocs builds n DocumentInfo entries with stable,
// sortable IDs so pagination tests are deterministic.
// IDs are zero-padded so lexicographic sort matches
// numeric sort.
func makeDocs(n int) []models.DocumentInfo {
	out := make([]models.DocumentInfo, n)
	for i := 0; i < n; i++ {
		out[i] = models.DocumentInfo{
			ID:    fmt.Sprintf("doc-%05d", i),
			UID:   fmt.Sprintf("u-%05d", i),
			Title: fmt.Sprintf("Document %d", i),
		}
	}
	return out
}

// TestHumaAPI_ListDocuments_DefaultLimit_25 pins the
// backward-compatible default page size: an existing
// client that hits GET /api/documents with no query
// params gets the FIRST 25 documents, not everything.
// Without a default, a 10k-doc corpus would still try
// to serialize every row on every page load — which
// is exactly what #14 is supposed to fix.
func TestHumaAPI_ListDocuments_DefaultLimit_25(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: makeDocs(50)}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Documents []models.DocumentInfo `json:"documents"`
		Total     int                   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.Documents, 25,
		"default limit must be 25 (got %d)", len(resp.Documents))
	require.Equal(t, 50, resp.Total,
		"total must reflect full corpus size, not the page size")
}

// TestHumaAPI_ListDocuments_LimitOffset pins the
// canonical pagination behavior: ?limit=50&offset=100
// returns at most 50 documents starting at position 100.
// Total stays at the full corpus size so the client can
// compute page counts.
func TestHumaAPI_ListDocuments_LimitOffset(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: makeDocs(200)}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents?limit=50&offset=100")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Documents []models.DocumentInfo `json:"documents"`
		Total     int                   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.Documents, 50,
		"limit=50 must return exactly 50 docs (got %d)", len(resp.Documents))
	require.Equal(t, 200, resp.Total)
	require.Equal(t, "doc-00100", resp.Documents[0].ID,
		"first doc on page 3 (offset 100, limit 50) must be doc-00100")
	require.Equal(t, "doc-00149", resp.Documents[49].ID,
		"last doc on page 3 must be doc-00149")
}

// TestHumaAPI_ListDocuments_OffsetBeyondTotal_ReturnsEmpty
// pins the boundary: when offset >= total, the page is
// empty but total still reports the full corpus size.
func TestHumaAPI_ListDocuments_OffsetBeyondTotal_ReturnsEmpty(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: makeDocs(10)}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents?limit=10&offset=100")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Documents []models.DocumentInfo `json:"documents"`
		Total     int                   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Empty(t, resp.Documents,
		"offset past total must return empty page (got %d)", len(resp.Documents))
	require.Equal(t, 10, resp.Total,
		"total must still report the full corpus size")
}

// TestHumaAPI_ListDocuments_LimitClampsToTotal pins
// the upper boundary: a limit that exceeds the corpus
// size returns what's left, not a 400.
func TestHumaAPI_ListDocuments_LimitClampsToTotal(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: makeDocs(10)}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents?limit=100&offset=0")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Documents []models.DocumentInfo `json:"documents"`
		Total     int                   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.Documents, 10,
		"limit past total must clamp to actual corpus size (got %d)", len(resp.Documents))
}

// TestHumaAPI_ListDocuments_NegativeOffsetTreatedAsZero
// pins the input-validation rule: a negative offset is
// nonsensical; treat it as 0 instead of erroring. The
// HTTP layer never sees a 400 for malformed paging args
// — clamp at the boundary, return what's available.
func TestHumaAPI_ListDocuments_NegativeOffsetTreatedAsZero(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: makeDocs(30)}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents?limit=10&offset=-5")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Documents []models.DocumentInfo `json:"documents"`
		Total     int                   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.Documents, 10,
		"negative offset must clamp to 0 (got %d docs)", len(resp.Documents))
	require.Equal(t, "doc-00000", resp.Documents[0].ID,
		"negative offset must clamp to 0 — first doc must be doc-00000")
}

// TestHumaAPI_ListDocuments_EmptyCorpus pins the
// zero-corpus case: empty list, total 0, 200 (not 404).
func TestHumaAPI_ListDocuments_EmptyCorpus(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: nil}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Documents []models.DocumentInfo `json:"documents"`
		Total     int                   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Empty(t, resp.Documents)
	require.Equal(t, 0, resp.Total)
}

// TestHumaAPI_ListDocuments_DeterministicOrdering pins
// the stable-order contract: pagination only makes sense
// if the same offset always returns the same first row.
// Without a deterministic sort, "doc-00100" at offset 100
// could be different rows on different requests.
func TestHumaAPI_ListDocuments_DeterministicOrdering(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: makeDocs(50)}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	first := api.Get("/api/documents?limit=10&offset=20")
	second := api.Get("/api/documents?limit=10&offset=20")
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)

	var resp1, resp2 struct {
		Documents []models.DocumentInfo `json:"documents"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &resp1))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &resp2))

	require.Len(t, resp1.Documents, 10)
	require.Len(t, resp2.Documents, 10)
	require.Equal(t, resp1.Documents[0].ID, resp2.Documents[0].ID,
		"first doc at offset 20 must be identical across requests")
	require.Equal(t, "doc-00020", resp1.Documents[0].ID,
		"first doc at offset 20 must be doc-00020")
}

// TestHumaAPI_ListDocuments_LimitTooLarge_ClampsTo1000
// pins the safety rail: a caller asking for limit=10000
// can't pin the server to a 10k-row response. Clamp to
// 1000 (the same cap the ingest search uses).
func TestHumaAPI_ListDocuments_LimitTooLarge_ClampsTo1000(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: makeDocs(500)}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents?limit=10000&offset=0")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Documents []models.DocumentInfo `json:"documents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.LessOrEqual(t, len(resp.Documents), 1000,
		"limit > 1000 must clamp to 1000 (got %d)", len(resp.Documents))
}

// TestHumaAPI_ListDocuments_PruneStillSeesAllDocuments
// pins the prune-endpoint contract: even though the
// list endpoint paginates, the prune endpoint must
// still see every document so its keep/delete plan is
// correct. This test guards against an accidental
// refactor that swaps ListDocuments for the paginated
// version inside the prune handler.
func TestHumaAPI_ListDocuments_PruneStillSeesAllDocuments(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: makeDocs(100)}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/documents/prune", map[string]any{
		"keep_uids": []string{"u-00000"},
		"dry_run":   true,
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Deleted  []string `json:"deleted_document_ids"`
		Kept     []string `json:"kept_document_ids"`
		NotFound []string `json:"not_found_document_ids"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.Deleted, 99,
		"prune must see all 100 docs even though list is paginated (got %d to-delete)",
		len(resp.Deleted))
	require.Equal(t, []string{"u-00000"}, resp.Kept)
}
