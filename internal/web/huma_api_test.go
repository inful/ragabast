package web

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHumaAPI_Health(t *testing.T) {
	_, api := humatest.New(t)
	registerHumaOperations(api, &fakeHumaService{}, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/health")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "healthy")
}

func TestHumaAPI_Ingest_ValidatesContent(t *testing.T) {
	_, api := humatest.New(t)
	registerHumaOperations(api, &fakeHumaService{}, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/ingest", map[string]any{"content": ""})
	require.Equal(t, 400, w.Code)
}

func TestHumaAPI_IngestRaw_AcceptsMarkdownBody(t *testing.T) {
	h, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	body := []byte("---\nuid: a\nurls:\n  - https://example.com\n---\n\n# Title\nHi\n")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/api/ingest/raw", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/markdown")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, string(body), svc.lastIngestContent)
}

func TestHumaAPI_IngestFile_AcceptsMultipartUpload(t *testing.T) {
	h, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	fileBytes := []byte("---\nuid: a\nurls:\n  - https://example.com\n---\n\n# Title\nHi\n")

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, err := w.CreateFormFile("file", "sample.md")
	require.NoError(t, err)
	_, err = part.Write(fileBytes)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/api/ingest/file", &b)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, string(fileBytes), svc.lastIngestContent)
}

func TestHumaAPI_Query_ReturnsLinks(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{debug: &service.QueryDebugInfo{Results: []models.SearchResult{{DocumentURLs: []string{"https://example.com/a"}}}}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/query", map[string]any{"query": "what is ragabast?", "top_k": 5})
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "Links")
	require.Contains(t, w.Body.String(), "https://example.com/a")
}

func TestHumaAPI_Query_ForwardsHistory(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{debug: &service.QueryDebugInfo{Results: []models.SearchResult{{DocumentURLs: []string{"https://example.com/a"}}}}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/query", map[string]any{
		"query": "How do I deploy it?",
		"top_k": 5,
		"history": []map[string]any{
			{"role": "user", "content": "We are talking about ragabast."},
			{"role": "assistant", "content": "Ok."},
		},
	})
	require.Equal(t, 200, w.Code)
	require.Len(t, svc.lastQueryOpts.History, 2)
	require.Equal(t, "user", svc.lastQueryOpts.History[0].Role)
	require.Contains(t, svc.lastQueryOpts.History[0].Content, "ragabast")
}

func TestHumaAPI_Ingest_Returns429WithRetryAfterWhenSaturated(t *testing.T) {
	_, api := humatest.New(t)
	limiter := NewIngestLimiter(1, 2*time.Second)
	require.True(t, limiter.TryAcquire())
	registerHumaOperations(api, &fakeHumaService{}, limiter, 0, nil)

	w := api.Post("/api/ingest", map[string]any{"content": "---\nuid: a\n---\n\n# Title\nHi\n"})
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "2", w.Header().Get("Retry-After"))
}

func TestHumaAPI_LinkSuggestions_DedupesAndLimits(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{searchResults: []models.SearchResult{
		{Similarity: 0.9, DocumentURLs: []string{"https://example.com/a", "https://example.com/b"}},
		{Similarity: 0.8, DocumentURLs: []string{"https://example.com/b", "https://example.com/c"}},
		{Similarity: 0.7, DocumentURLs: []string{"https://example.com/d"}},
	}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/link-suggestions", map[string]any{
		"text":      "some section text",
		"top_k":     10,
		"max_urls":  3,
		"min_score": 0.0,
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		URLs []string `json:"urls"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"}, resp.URLs)
}

func TestHumaAPI_LinkSuggestions_RespectsMinScore(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{searchResults: []models.SearchResult{
		{Similarity: 0.9, DocumentURLs: []string{"https://example.com/a"}},
		{Similarity: 0.4, DocumentURLs: []string{"https://example.com/b"}},
	}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/link-suggestions", map[string]any{
		"text":      "some section text",
		"min_score": 0.5,
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		URLs []string `json:"urls"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []string{"https://example.com/a"}, resp.URLs)
}

func TestHumaAPI_LinkSuggestions_ValidatesText(t *testing.T) {
	_, api := humatest.New(t)
	registerHumaOperations(api, &fakeHumaService{}, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/link-suggestions", map[string]any{"text": ""})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHumaAPI_DeleteDocument_DeletesKnownDocument(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: []models.DocumentInfo{{ID: "doc-1", UID: "u-1"}}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Delete("/api/documents/doc-1")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"doc-1"}, svc.deletedIDs)
}

func TestHumaAPI_DeleteDocument_Returns404ForUnknownDocument(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: []models.DocumentInfo{{ID: "doc-1", UID: "u-1"}}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Delete("/api/documents/missing")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, svc.deletedIDs)
}

func TestHumaAPI_PruneDocuments_DeletesEverythingExceptKeptUIDs(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: []models.DocumentInfo{
		{ID: "doc-1", UID: "keep"},
		{ID: "doc-2", UID: "drop"},
		{ID: "doc-3", UID: "drop"},
	}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/documents/prune", map[string]any{"keep_uids": []string{"keep"}})
	require.Equal(t, http.StatusOK, w.Code)
	require.ElementsMatch(t, []string{"doc-2", "doc-3"}, svc.deletedIDs)
}

func TestHumaAPI_PruneDocuments_DryRunDoesNotDelete(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: []models.DocumentInfo{{ID: "doc-1", UID: "u-1"}}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/documents/prune", map[string]any{"delete_document_ids": []string{"doc-1"}, "dry_run": true})
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, svc.deletedIDs)
}

func TestHumaAPI_FrontmatterSuggest_MergesAndGuards(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{frontmatterSug: service.FrontmatterSuggestion{
		Description: "Suggested description.",
		Categories:  []string{"Guides", "INVALID"},
		Tags:        []string{"go", "rag"},
		CustomTags:  []string{"custom-tag"},
	}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/frontmatter/suggest", map[string]any{
		"content":            "---\nuid: u-1\ndescription: existing desc\ntags:\n  - existing\nother: keepme\n---\n\n# Title\nHello\n",
		"allowed_categories": []string{"Guides", "Reference"},
		"allowed_tags":       []string{"go", "rag"},
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Frontmatter map[string]any `json:"frontmatter"`
		Applied     map[string]any `json:"applied"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Existing frontmatter preserved.
	require.Equal(t, "keepme", resp.Frontmatter["other"])
	// Existing description not overwritten.
	require.Equal(t, "existing desc", resp.Frontmatter["description"])

	// Categories filtered to allowed list and merged.
	cats, ok := resp.Frontmatter["categories"].([]any)
	require.True(t, ok)
	require.Contains(t, cats, "Guides")
	require.NotContains(t, cats, "INVALID")

	// Tags merged (existing + allowed + custom).
	tags, ok := resp.Frontmatter["tags"].([]any)
	require.True(t, ok)
	require.Contains(t, tags, "existing")
	require.Contains(t, tags, "go")
	require.Contains(t, tags, "rag")
	require.Contains(t, tags, "custom-tag")

	// Applied should mention that we did not overwrite description.
	require.Equal(t, false, resp.Applied["description_set"])
}

func TestHumaAPI_FrontmatterSuggest_MergesExistingWithAllowed(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{frontmatterSug: service.FrontmatterSuggestion{
		Description: "New description.",
		Categories:  []string{"guides"},
		Tags:        []string{"go", "newtag"},
		CustomTags:  nil,
	}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	// The service should receive merged allowed lists that include existing tags/categories
	// This test verifies that existing tags and categories are added to the allowed lists
	w := api.Post("/api/frontmatter/suggest", map[string]any{
		"content":            "---\nuid: u-1\ncategories:\n  - Reference\ntags:\n  - existing\n---\n\n# Title\nHello\n",
		"allowed_categories": []string{"Guides", "Reference"},
		"allowed_tags":       []string{"go", "rag"},
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Frontmatter map[string]any `json:"frontmatter"`
		Applied     map[string]any `json:"applied"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Verify the final result includes both existing and new
	cats, ok := resp.Frontmatter["categories"].([]any)
	require.True(t, ok)
	require.Contains(t, cats, "Guides")    // From LLM suggestion (allowed)
	require.Contains(t, cats, "Reference") // From existing

	tags, ok := resp.Frontmatter["tags"].([]any)
	require.True(t, ok)
	require.Contains(t, tags, "existing") // From existing
	require.Contains(t, tags, "go")       // From LLM suggestion (allowed)
	require.Contains(t, tags, "newtag")   // From LLM suggestion (allowed)
}

func TestHumaAPI_FrontmatterSuggest_ValidatesInputs(t *testing.T) {
	_, api := humatest.New(t)
	registerHumaOperations(api, &fakeHumaService{}, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/frontmatter/suggest", map[string]any{
		"content":            "# Hi",
		"allowed_categories": []string{},
		"allowed_tags":       []string{"go"},
	})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHumaAPI_FrontmatterSuggest_CanonicalizesAllowedAndKeepsCustomTags(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{frontmatterSug: service.FrontmatterSuggestion{
		Description: "",
		Categories:  []string{"guides"},
		Tags:        []string{"RAG", "NewTag"},
		CustomTags:  nil,
	}}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Post("/api/frontmatter/suggest", map[string]any{
		"content":            "---\nuid: u-1\n---\n\n# Title\nHello\n",
		"allowed_categories": []string{"Guides", "Reference"},
		"allowed_tags":       []string{"go", "rag"},
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Frontmatter map[string]any `json:"frontmatter"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	cats, ok := resp.Frontmatter["categories"].([]any)
	require.True(t, ok)
	require.Contains(t, cats, "Guides")

	tags, ok := resp.Frontmatter["tags"].([]any)
	require.True(t, ok)
	require.Contains(t, tags, "rag")
	require.Contains(t, tags, "newtag")
}

func TestHumaAPI_GetTags_ReturnsNormalizedTags(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/tags")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Tags []string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []string{"go", "rag", "api"}, resp.Tags)
}

func TestHumaAPI_GetCategories_ReturnsNormalizedCategories(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/categories")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Categories []string `json:"categories"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []string{"Guides", "Reference", "Tutorials"}, resp.Categories)
}

func TestHumaAPI_GetTagsAndCategories_ReturnsBoth(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{}
	registerHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second), 0, nil)

	w := api.Get("/api/tags-categories")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Tags       []string `json:"tags"`
		Categories []string `json:"categories"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []string{"go", "rag", "api"}, resp.Tags)
	require.Equal(t, []string{"Guides", "Reference", "Tutorials"}, resp.Categories)
}
