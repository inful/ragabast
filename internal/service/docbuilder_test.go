package service

import (
	"context"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// withRagabastCfg builds a minimal valid Config with the given
// Ragabast.DocbuilderBaseURL and the rest filled with defaults
// from config.DefaultConfig(). The returned Config is safe to
// pass to NewService.
func withRagabastCfg(baseURL string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Ragabast.DocbuilderBaseURL = baseURL
	return cfg
}

func TestBuildDocbuilderURL_EmptyBaseReturnsEmpty(t *testing.T) {
	svc := &Service{config: withRagabastCfg("")}
	require.Empty(t, svc.buildDocbuilderURL("any-uid"))
}

func TestBuildDocbuilderURL_EmptyUIDReturnsEmpty(t *testing.T) {
	svc := &Service{config: withRagabastCfg("https://docs.example.com")}
	require.Empty(t, svc.buildDocbuilderURL(""))
}

func TestBuildDocbuilderURL_ConcatenatesWithSingleSlash(t *testing.T) {
	svc := &Service{config: withRagabastCfg("https://docs.example.com")}
	require.Equal(t, "https://docs.example.com/_uid/adr-001/", svc.buildDocbuilderURL("adr-001"))
}

func TestBuildDocbuilderURL_ToleratesTrailingSlashOnBase(t *testing.T) {
	svc := &Service{config: withRagabastCfg("https://docs.example.com/")}
	require.Equal(t, "https://docs.example.com/_uid/adr-001/", svc.buildDocbuilderURL("adr-001"))
}

func TestBuildDocbuilderURL_ToleratesLeadingSlashOnUID(t *testing.T) {
	svc := &Service{config: withRagabastCfg("https://docs.example.com")}
	require.Equal(t, "https://docs.example.com/_uid/adr-001/", svc.buildDocbuilderURL("/adr-001"))
}

func TestEnrichWithDocbuilderURLs_PopulatesEachResultWithUID(t *testing.T) {
	svc := &Service{config: withRagabastCfg("https://docs.example.com")}
	results := []models.SearchResult{
		{ChunkID: "c1", DocumentID: "d1", UID: "adr-001"},
		{ChunkID: "c2", DocumentID: "d2", UID: "adr-002"},
	}
	svc.enrichWithDocbuilderURLs(results)
	require.Equal(t, "https://docs.example.com/_uid/adr-001/", results[0].DocbuilderURL)
	require.Equal(t, "https://docs.example.com/_uid/adr-002/", results[1].DocbuilderURL)
}

func TestEnrichWithDocbuilderURLs_SkipsResultsWithoutUID(t *testing.T) {
	svc := &Service{config: withRagabastCfg("https://docs.example.com")}
	results := []models.SearchResult{
		{ChunkID: "c1", DocumentID: "d1", UID: ""}, // no UID — nothing to link to
		{ChunkID: "c2", DocumentID: "d2", UID: "adr-002"},
	}
	svc.enrichWithDocbuilderURLs(results)
	require.Empty(t, results[0].DocbuilderURL,
		"results without a UID must not get a docbuilder URL even when base is set")
	require.Equal(t, "https://docs.example.com/_uid/adr-002/", results[1].DocbuilderURL)
}

func TestEnrichWithDocbuilderURLs_NoBaseNoOp(t *testing.T) {
	svc := &Service{config: withRagabastCfg("")}
	results := []models.SearchResult{
		{ChunkID: "c1", DocumentID: "d1", UID: "adr-001"},
	}
	svc.enrichWithDocbuilderURLs(results)
	require.Empty(t, results[0].DocbuilderURL,
		"no base URL = no enrichment, even when UID is set")
}

func TestEnrichDocInfos_PopulatesEachInfoWithUID(t *testing.T) {
	svc := &Service{config: withRagabastCfg("https://docs.example.com")}
	infos := []models.DocumentInfo{
		{ID: "d1", UID: "adr-001"},
		{ID: "d2", UID: "adr-002"},
	}
	svc.enrichDocInfos(infos)
	require.Equal(t, "https://docs.example.com/_uid/adr-001/", infos[0].DocbuilderURL)
	require.Equal(t, "https://docs.example.com/_uid/adr-002/", infos[1].DocbuilderURL)
}

func TestEnrichDocInfos_NoBaseNoOp(t *testing.T) {
	svc := &Service{config: withRagabastCfg("")}
	infos := []models.DocumentInfo{
		{ID: "d1", UID: "adr-001"},
	}
	svc.enrichDocInfos(infos)
	require.Empty(t, infos[0].DocbuilderURL)
}

// TestExtractURLs_IncludesDocbuilderURL pins that the link
// suggestions endpoint includes the synthetic permalink in its
// output. Without it, the only URLs the LLM sees when reading
// link suggestions are the doc's own frontmatter urls — which
// defeats the whole point of having a docbuilder base URL
// configured.
func TestExtractURLs_IncludesDocbuilderURL(t *testing.T) {
	results := []models.SearchResult{
		{
			ChunkID: "c1", DocumentID: "d1", UID: "adr-001",
			DocumentURLs:  []string{"https://example.com/legacy-link"},
			DocbuilderURL: "https://docs.example.com/_uid/adr-001/",
		},
	}
	got := extractURLs(results)
	require.Contains(t, got, "https://docs.example.com/_uid/adr-001/",
		"extractURLs must include the synthetic docbuilder permalink")
	require.Contains(t, got, "https://example.com/legacy-link",
		"extractURLs must continue to include the doc's own frontmatter URLs")
}

// TestCollectContextURLs_OrdersDocbuilderBeforeBody pins the
// ordering contract for the LLM context: frontmatter URLs
// first, synthetic docbuilder URL second, body URLs last.
// The docbuilder URL is reliable in a way the body's bare URLs
// are not — if the LLM picks one to cite, we want the stable
// one.
func TestCollectContextURLs_OrdersDocbuilderBeforeBody(t *testing.T) {
	result := models.SearchResult{
		ChunkID: "c1", DocumentID: "d1", UID: "adr-001",
		DocumentURLs:  []string{"https://example.com/frontmatter"},
		DocbuilderURL: "https://docs.example.com/_uid/adr-001/",
		Content:       "see https://example.com/in-body-url for details",
	}
	got := collectContextURLs(result)
	require.Equal(t, []string{
		"https://example.com/frontmatter",
		"https://docs.example.com/_uid/adr-001/",
		"https://example.com/in-body-url",
	}, got)
}

// Reference the context import so the file builds even when
// the test set is reduced.
var _ = context.Background
