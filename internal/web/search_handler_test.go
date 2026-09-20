package web

import (
	stdjson "encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchTestAPI builds a humatest.TestAPI with just the search
// operation wired up, against the supplied fakeService. The
// fake captures the lastSearchMode so tests can assert what
// the handler plumbed through.
func searchTestAPI(t *testing.T, svc *fakeService) humatest.TestAPI {
	t.Helper()
	_, api := humatest.New(t, huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info:    &huma.Info{Title: "test", Version: "1.0.0"},
		},
		Formats: map[string]huma.Format{
			"application/json": huma.DefaultJSONFormat,
			"json":             huma.DefaultJSONFormat,
		},
		DefaultFormat: "application/json",
	})
	registerSearchOperation(api, svc)
	return api
}

// TestSearch_DefaultsToHybrid pins the v0.4.0 behavior:
// an absent `mode` field on the request routes through
// HybridSearch with ModeHybrid.
func TestSearch_DefaultsToHybrid(t *testing.T) {
	t.Parallel()

	svc := &fakeService{
		searchResults: []models.SearchResult{
			{ChunkID: "c1", DocumentID: "d1", DocumentTitle: "Doc", Similarity: 0.9},
		},
	}
	api := searchTestAPI(t, svc)

	resp := api.Post("/api/search", map[string]any{
		"query": "kubernetes ingress",
		"limit": 5,
	})

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, service.ModeHybrid, svc.lastSearchMode,
		"absent mode must default to hybrid (v0.4.0)")
}

// TestSearch_RespectsExplicitMode pins that the handler
// honors the explicit mode field on the wire.
func TestSearch_RespectsExplicitMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		wireValue string
		wantMode  service.SearchMode
	}{
		{"semantic", service.ModeSemantic},
		{"keyword", service.ModeKeyword},
		{"hybrid", service.ModeHybrid},
		{"", service.ModeHybrid}, // empty falls back to hybrid
	} {
		t.Run(tc.wireValue, func(t *testing.T) {
			svc := &fakeService{}
			api := searchTestAPI(t, svc)

			body := map[string]any{"query": "q", "limit": 5}
			if tc.wireValue != "" {
				body["mode"] = tc.wireValue
			}
			resp := api.Post("/api/search", body)

			require.Equal(t, http.StatusOK, resp.Code)
			assert.Equal(t, tc.wantMode, svc.lastSearchMode,
				"wire value %q should map to %v", tc.wireValue, tc.wantMode)
		})
	}
}

// TestSearch_RejectsUnknownMode pins that Huma's enum
// validator catches garbage values at the wire. Unknown
// modes return 422 — the handler's internal fallback to
// hybrid is a defense-in-depth measure, not a way to mask
// typos.
func TestSearch_RejectsUnknownMode(t *testing.T) {
	t.Parallel()

	svc := &fakeService{}
	api := searchTestAPI(t, svc)

	resp := api.Post("/api/search", map[string]any{
		"query": "q",
		"limit": 5,
		"mode":  "bogus",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code,
		"unknown mode must return 422, not silently default to hybrid")
}

// TestSearch_FiltersReachesService pins that the
// document_id / tag / category fields on the request body
// are forwarded to SearchFilters unchanged.
func TestSearch_FiltersReachesService(t *testing.T) {
	t.Parallel()

	svc := &fakeService{}
	api := searchTestAPI(t, svc)

	resp := api.Post("/api/search", map[string]any{
		"query":       "kubernetes",
		"limit":       5,
		"document_id": "doc-a",
		"tag":         "security",
		"category":    "tutorial",
	})

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "doc-a", svc.lastSearchFilters.DocumentID)
	assert.Equal(t, "security", svc.lastSearchFilters.Tag)
	assert.Equal(t, "tutorial", svc.lastSearchFilters.Category)
}

// TestSearch_DefaultMinScore pins that the v0.4.0 handler
// no longer applies a 0.5 floor to min_score (the previous
// default filtered out too many results from
// nomic-embed-text's narrower cosine distribution).
func TestSearch_DefaultMinScoreIsZero(t *testing.T) {
	t.Parallel()

	// Feed in three results: 0.1, 0.4, 0.8. The v0.3.0 default
	// of 0.5 would drop the first two. The v0.4.0 default of
	// 0 keeps all three.
	svc := &fakeService{
		searchResults: []models.SearchResult{
			{ChunkID: "c1", Similarity: 0.1},
			{ChunkID: "c2", Similarity: 0.4},
			{ChunkID: "c3", Similarity: 0.8},
		},
	}
	api := searchTestAPI(t, svc)

	resp := api.Post("/api/search", map[string]any{"query": "q", "limit": 5})

	require.Equal(t, http.StatusOK, resp.Code)
	var body searchResponseBody
	require.NoError(t, jsonDecode(resp, &body))
	assert.Equal(t, 3, body.Count,
		"all three results should pass when min_score defaults to 0")
}

// TestSearch_RejectsEmptyQuery pins the 400 Bad Request
// path. The handler must refuse an empty query string
// rather than pass it through (where it would 500 against
// the embeddings call).
func TestSearch_RejectsEmptyQuery(t *testing.T) {
	t.Parallel()

	svc := &fakeService{}
	api := searchTestAPI(t, svc)

	resp := api.Post("/api/search", map[string]any{"query": "", "limit": 5})
	require.Equal(t, http.StatusBadRequest, resp.Code)
}

// jsonDecode is a tiny shim that unmarshals the test response
// into the named struct. Huma's test API uses a non-standard
// writer so we go through it carefully.
func jsonDecode(resp *httptest.ResponseRecorder, dst any) error {
	return stdjson.Unmarshal(resp.Body.Bytes(), dst)
}
