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

type fakeHumaService struct {
	healthErr         error
	ingested          *models.Document
	ingestErr         error
	lastIngestContent string
	searchResults     []models.SearchResult
	searchErr         error
	documents         []models.DocumentInfo
	deleteErr         error
	deletedIDs        []string
	answer            string
	debug             *service.QueryDebugInfo
	queryErr          error
	lastQueryOpts     service.LLMOptions
	frontmatterSug    service.FrontmatterSuggestion
	frontmatterErr    error
}

func (f *fakeHumaService) CheckHealth(ctx context.Context) (bool, error) {
	return f.healthErr == nil, f.healthErr
}

func (f *fakeHumaService) IngestDocument(ctx context.Context, content string) (*models.Document, error) {
	if f.ingestErr != nil {
		return nil, f.ingestErr
	}
	f.lastIngestContent = content
	if f.ingested != nil {
		return f.ingested, nil
	}
	return &models.Document{ID: "doc-1", Chunks: []models.Chunk{{}, {}}}, nil
}

func (f *fakeHumaService) Search(ctx context.Context, query string, limit int, filters map[string]string) ([]models.SearchResult, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if limit <= 0 || limit >= len(f.searchResults) {
		return f.searchResults, nil
	}
	return f.searchResults[:limit], nil
}

func (f *fakeHumaService) ListDocuments(ctx context.Context) ([]models.DocumentInfo, error) {
	return f.documents, nil
}

func (f *fakeHumaService) DeleteDocument(ctx context.Context, documentID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedIDs = append(f.deletedIDs, documentID)
	return nil
}

func (f *fakeHumaService) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error) {
	f.lastQueryOpts = opts
	if f.queryErr != nil {
		return "", nil, f.queryErr
	}
	if f.debug == nil {
		f.debug = &service.QueryDebugInfo{Results: []models.SearchResult{}}
	}
	if f.answer == "" {
		f.answer = "Answer.\n\nLinks:\n- https://example.com/a"
	}
	return f.answer, f.debug, nil
}

func (f *fakeHumaService) QueryWithLLM(ctx context.Context, query string, model string, history []struct {
	Role    string
	Content string
}) (string, []struct {
	ID      string
	Content string
	Score   float64
}, error,
) {
	return "", nil, nil
}

func (f *fakeHumaService) SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (service.FrontmatterSuggestion, error) {
	if f.frontmatterErr != nil {
		return service.FrontmatterSuggestion{}, f.frontmatterErr
	}
	return f.frontmatterSug, nil
}

func (f *fakeHumaService) GetNormalizedTags(ctx context.Context) ([]string, error) {
	return []string{"go", "rag", "api"}, nil
}

func (f *fakeHumaService) GetNormalizedCategories(ctx context.Context) ([]string, error) {
	return []string{"Guides", "Reference", "Tutorials"}, nil
}

func (f *fakeHumaService) GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error) {
	tags, err = f.GetNormalizedTags(ctx)
	if err != nil {
		return nil, nil, err
	}
	categories, err = f.GetNormalizedCategories(ctx)
	if err != nil {
		return nil, nil, err
	}
	return tags, categories, nil
}

func TestHumaAPI_Health(t *testing.T) {
	_, api := humatest.New(t)
	RegisterHumaOperations(api, &fakeHumaService{}, NewIngestLimiter(10, 1*time.Second))

	w := api.Get("/api/health")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "healthy")
}

func TestHumaAPI_Ingest_ValidatesContent(t *testing.T) {
	_, api := humatest.New(t)
	RegisterHumaOperations(api, &fakeHumaService{}, NewIngestLimiter(10, 1*time.Second))

	w := api.Post("/api/ingest", map[string]any{"content": ""})
	require.Equal(t, 400, w.Code)
}

func TestHumaAPI_IngestRaw_AcceptsMarkdownBody(t *testing.T) {
	h, api := humatest.New(t)
	svc := &fakeHumaService{}
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

	w := api.Post("/api/query", map[string]any{"query": "what is ragabast?", "top_k": 5})
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "Links")
	require.Contains(t, w.Body.String(), "https://example.com/a")
}

func TestHumaAPI_Query_ForwardsHistory(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{debug: &service.QueryDebugInfo{Results: []models.SearchResult{{DocumentURLs: []string{"https://example.com/a"}}}}}
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, &fakeHumaService{}, limiter)

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, &fakeHumaService{}, NewIngestLimiter(10, 1*time.Second))

	w := api.Post("/api/link-suggestions", map[string]any{"text": ""})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHumaAPI_DeleteDocument_DeletesKnownDocument(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: []models.DocumentInfo{{ID: "doc-1", UID: "u-1"}}}
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

	w := api.Delete("/api/documents/doc-1")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"doc-1"}, svc.deletedIDs)
}

func TestHumaAPI_DeleteDocument_Returns404ForUnknownDocument(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: []models.DocumentInfo{{ID: "doc-1", UID: "u-1"}}}
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

	w := api.Post("/api/documents/prune", map[string]any{"keep_uids": []string{"keep"}})
	require.Equal(t, http.StatusOK, w.Code)
	require.ElementsMatch(t, []string{"doc-2", "doc-3"}, svc.deletedIDs)
}

func TestHumaAPI_PruneDocuments_DryRunDoesNotDelete(t *testing.T) {
	_, api := humatest.New(t)
	svc := &fakeHumaService{documents: []models.DocumentInfo{{ID: "doc-1", UID: "u-1"}}}
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, &fakeHumaService{}, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
	RegisterHumaOperations(api, svc, NewIngestLimiter(10, 1*time.Second))

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
