package web

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/vector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHumaAPI_DocumentRelated_ReturnsRelatedUIDs pins the endpoint:
// a UID in, the UIDs of the most similar documents out, most
// similar first.
func TestHumaAPI_DocumentRelated_ReturnsRelatedUIDs(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		related: []vector.RelatedDocument{
			{UID: "adr-002", Title: "Second ADR", Score: 0.91},
			{UID: "adr-003", Title: "Third ADR", Score: 0.72},
		},
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents/adr-001/related")
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		UID     string `json:"uid"`
		Related []struct {
			UID   string  `json:"uid"`
			Title string  `json:"title"`
			Score float64 `json:"score"`
		} `json:"related"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.Equal(t, "adr-001", body.UID)
	require.Len(t, body.Related, 2)
	assert.Equal(t, "adr-002", body.Related[0].UID)
	assert.Equal(t, "adr-003", body.Related[1].UID)
	assert.InDelta(t, 0.91, body.Related[0].Score, 0.001)
}

// TestHumaAPI_DocumentRelated_UnknownUIDIs404 pins the "no such
// document" signal.
func TestHumaAPI_DocumentRelated_UnknownUIDIs404(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{relatedUID: "other-uid"}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents/adr-001/related")
	require.Equal(t, http.StatusNotFound, w.Code)
}

// TestHumaAPI_DocumentRelated_DefaultLimitIsFive pins the default
// page size: the caller asks for nothing and gets five.
func TestHumaAPI_DocumentRelated_DefaultLimitIsFive(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		related: []vector.RelatedDocument{
			{UID: "a", Score: 0.9},
			{UID: "b", Score: 0.8},
			{UID: "c", Score: 0.7},
			{UID: "d", Score: 0.6},
			{UID: "e", Score: 0.5},
			{UID: "f", Score: 0.4},
		},
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/documents/adr-001/related")
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Related []struct {
			UID string `json:"uid"`
		} `json:"related"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Related, 5, "the default page size is five")
}
