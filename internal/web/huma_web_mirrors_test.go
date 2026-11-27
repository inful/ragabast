package web

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHumaAPI_WebMirrors_ListsTheEffectiveList pins the operator's
// view of the mirroring: the allowed hosts and the pages that were
// requested, allowed, and fetched.
func TestHumaAPI_WebMirrors_ListsTheEffectiveList(t *testing.T) {
	router, api := humatest.New(t)
	svc := &fakeHumaService{
		allowedWebHosts: []string{"example.com"},
		webMirrors: []service.WebMirror{
			{
				SourceURL:  "https://example.com/spec",
				UID:        "web:example.com-spec",
				Title:      "Semantic Versioning",
				DeclaredBy: []string{"doc-1"},
			},
		},
	}
	registerHumaOperations(router, api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/web-mirrors")
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		AllowedHosts []string `json:"allowed_hosts"`
		Mirrors      []struct {
			SourceURL  string   `json:"source_url"`
			UID        string   `json:"uid"`
			DeclaredBy []string `json:"declared_by"`
		} `json:"mirrors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.Equal(t, []string{"example.com"}, body.AllowedHosts)
	require.Len(t, body.Mirrors, 1)
	assert.Equal(t, "https://example.com/spec", body.Mirrors[0].SourceURL)
	assert.Equal(t, "web:example.com-spec", body.Mirrors[0].UID)
	assert.Equal(t, []string{"doc-1"}, body.Mirrors[0].DeclaredBy)
}
