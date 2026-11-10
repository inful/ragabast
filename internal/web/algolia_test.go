package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// algoliaFakeService wraps fakeHumaService and honors the search
// limit, so the pagination behavior of the compat layer can be
// exercised. The shared fake ignores the limit because every
// existing caller passes the limit it wants anyway.
type algoliaFakeService struct {
	*fakeHumaService
	lastLimit int
}

func (f *algoliaFakeService) HybridSearch(
	ctx context.Context,
	query string,
	limit int,
	filters service.SearchFilters,
	mode service.SearchMode,
) ([]models.SearchResult, error) {
	f.lastLimit = limit
	results, err := f.fakeHumaService.HybridSearch(ctx, query, limit, filters, mode)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// algoliaSampleResults returns two chunks that look like a real
// DocSearch corpus: a page with a heading path and a citation URL.
func algoliaSampleResults() []models.SearchResult {
	return []models.SearchResult{
		{
			ChunkID:       "chunk-1",
			DocumentID:    "doc-1",
			Content:       "Install the package with the install command.",
			HeaderPath:    "Setup Guide > Installation",
			DocumentTitle: "Setup Guide",
			CitationURL:   "https://docs.example.com/setup#installation",
			Similarity:    0.9,
		},
		{
			ChunkID:       "chunk-2",
			DocumentID:    "doc-2",
			Content:       "Unrelated content about widgets.",
			HeaderPath:    "Widget Manual",
			DocumentTitle: "Widget Manual",
			CitationURL:   "https://docs.example.com/widgets",
			Similarity:    0.4,
		},
	}
}

// algoliaNewServer builds a server wired to the fake service.
func algoliaNewServer(t *testing.T, svc serviceAPI) *Server {
	t.Helper()
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	return NewServer(cfg, svc)
}

// algoliaMultiBody builds the request body the DocSearch client
// sends: a batch whose entries carry the query next to a
// URL-encoded params string.
func algoliaMultiBody(t *testing.T, query string, params url.Values) string {
	t.Helper()
	body := map[string]any{
		"requests": []map[string]any{{
			"query":     query,
			"indexName": "ragabast",
			"params":    params.Encode(),
		}},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return string(raw)
}

// algoliaPost posts a body to the compat endpoint the way the
// DocSearch client does: JSON payload labeled as
// application/x-www-form-urlencoded.
func algoliaPost(t *testing.T, s *Server, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

// TestAlgoliaDocSearch_MultiQuery_MatchesDocSearchContract pins
// the exact request/response shape the DocSearch JS client
// expects: POST /1/indexes/*/queries with a batched body, answered
// with {"results": [...]} where each result carries the Algolia
// envelope and DocSearch-shaped hits.
func TestAlgoliaDocSearch_MultiQuery_MatchesDocSearchContract(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := algoliaNewServer(t, svc)

	params := url.Values{}
	params.Set("attributesToRetrieve", `["hierarchy.lvl0","hierarchy.lvl1","content","type","url"]`)
	params.Set("attributesToSnippet", `["hierarchy.lvl1:5","content:5"]`)
	params.Set("snippetEllipsisText", "…")
	params.Set("highlightPreTag", "<mark>")
	params.Set("highlightPostTag", "</mark>")
	params.Set("hitsPerPage", "20")

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "install", params), nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Results []struct {
			Hits             []map[string]any `json:"hits"`
			NbHits           int              `json:"nbHits"`
			Page             int              `json:"page"`
			NbPages          int              `json:"nbPages"`
			HitsPerPage      int              `json:"hitsPerPage"`
			ProcessingTimeMS int              `json:"processingTimeMS"`
			Query            string           `json:"query"`
			Index            string           `json:"index"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Results, 1)

	result := resp.Results[0]
	require.Equal(t, "install", result.Query)
	require.Equal(t, "ragabast", result.Index)
	require.Equal(t, 20, result.HitsPerPage)
	require.Equal(t, 2, result.NbHits)
	require.Equal(t, 1, result.NbPages)
	require.Len(t, result.Hits, 2)

	hit := result.Hits[0]
	require.Equal(t, "chunk-1", hit["objectID"])
	require.Equal(t, "content", hit["type"])
	require.Equal(t, "https://docs.example.com/setup#installation", hit["url"])
	require.Equal(t, "Install the package with the install command.", hit["content"])

	hierarchy, ok := hit["hierarchy"].(map[string]any)
	require.True(t, ok, "hierarchy must be an object")
	require.Equal(t, "Setup Guide", hierarchy["lvl0"], "lvl0 is the page title")
	require.Equal(t, "Installation", hierarchy["lvl1"], "lvl1 is the heading below the title")
}

// TestAlgoliaDocSearch_HitDropsUnrequestedAttributes pins the
// attributesToRetrieve contract: Algolia returns only what the
// caller asked for, plus objectID.
func TestAlgoliaDocSearch_HitDropsUnrequestedAttributes(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := algoliaNewServer(t, svc)

	params := url.Values{}
	params.Set("attributesToRetrieve", `["content"]`)

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "install", params), nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Results []struct {
			Hits []map[string]any `json:"hits"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Results, 1)
	require.Len(t, resp.Results[0].Hits, 2)

	hit := resp.Results[0].Hits[0]
	require.Equal(t, "chunk-1", hit["objectID"], "objectID is always returned")
	require.Contains(t, hit, "content")
	require.NotContains(t, hit, "url", "url was not requested")
	require.NotContains(t, hit, "hierarchy", "hierarchy was not requested")
}

// TestAlgoliaDocSearch_HighlightsQueryTerms pins the
// _highlightResult map the DocSearch UI reads to bold the matched
// words.
func TestAlgoliaDocSearch_HighlightsQueryTerms(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := algoliaNewServer(t, svc)

	params := url.Values{}
	params.Set("highlightPreTag", "<mark>")
	params.Set("highlightPostTag", "</mark>")

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "install", params), nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Results []struct {
			Hits []map[string]any `json:"hits"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	highlight, ok := resp.Results[0].Hits[0]["_highlightResult"].(map[string]any)
	require.True(t, ok, "_highlightResult must be an object")

	content, ok := highlight["content"].(map[string]any)
	require.True(t, ok, "content highlight must be an object")
	require.Contains(t, content["value"], "<mark>install</mark>")

	hierarchy, ok := highlight["hierarchy"].(map[string]any)
	require.True(t, ok, "hierarchy highlight must be an object")
	lvl0, ok := hierarchy["lvl0"].(map[string]any)
	require.True(t, ok, "lvl0 highlight must be an object")
	require.Equal(t, "Setup Guide", lvl0["value"])
}

// TestAlgoliaDocSearch_SnippetsContent pins the _snippetResult
// map: a window of words around the match, marked with the
// ellipsis when the text is truncated.
func TestAlgoliaDocSearch_SnippetsContent(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := algoliaNewServer(t, svc)

	params := url.Values{}
	params.Set("attributesToSnippet", `["content:3"]`)
	params.Set("snippetEllipsisText", "…")
	params.Set("highlightPreTag", "<mark>")
	params.Set("highlightPostTag", "</mark>")

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "install", params), nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Results []struct {
			Hits []map[string]any `json:"hits"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	snippet, ok := resp.Results[0].Hits[0]["_snippetResult"].(map[string]any)
	require.True(t, ok, "_snippetResult must be an object")
	content, ok := snippet["content"].(map[string]any)
	require.True(t, ok, "content snippet must be an object")
	// The original casing of the matched word is preserved.
	require.Contains(t, content["value"], "<mark>Install</mark>")
}

// TestAlgoliaDocSearch_EscapesContent pins the XSS guard: the
// DocSearch client renders the snippet with
// dangerouslySetInnerHTML, so the chunk body must be escaped.
func TestAlgoliaDocSearch_EscapesContent(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: []models.SearchResult{{
		ChunkID:       "chunk-xss",
		Content:       `<script>alert("x")</script> install now`,
		DocumentTitle: "Evil",
	}}}}
	s := algoliaNewServer(t, svc)

	params := url.Values{}
	params.Set("attributesToSnippet", `["content:10"]`)

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "install", params), nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// The wire format escapes <, > and & for JSON, so the raw body
	// never carries a live tag. The decoded value must carry the
	// escaped text, not the raw markup.
	var resp struct {
		Results []struct {
			Hits []map[string]any `json:"hits"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Results, 1)
	require.Len(t, resp.Results[0].Hits, 1)

	hit := resp.Results[0].Hits[0]
	require.NotContains(t, hit["content"], "<script>")
	require.Contains(t, hit["content"], "&lt;script&gt;")

	snippet, ok := hit["_snippetResult"].(map[string]any)
	require.True(t, ok, "_snippetResult must be an object")
	content, ok := snippet["content"].(map[string]any)
	require.True(t, ok, "content snippet must be an object")
	require.NotContains(t, content["value"], "<script")
	require.Contains(t, content["value"], "&lt;/script&gt;")
}

// TestAlgoliaDocSearch_PaginationSlicesPages pins the page
// behavior: the backend is asked for the first (page+1) pages
// worth of hits and the requested page is sliced out of them.
func TestAlgoliaDocSearch_PaginationSlicesPages(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := algoliaNewServer(t, svc)

	params := url.Values{}
	params.Set("hitsPerPage", "1")
	params.Set("page", "1")

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "install", params), nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Results []struct {
			Hits        []map[string]any `json:"hits"`
			Page        int              `json:"page"`
			NbHits      int              `json:"nbHits"`
			HitsPerPage int              `json:"hitsPerPage"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	result := resp.Results[0]
	require.Equal(t, 1, result.Page)
	require.Equal(t, 1, result.HitsPerPage)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "chunk-2", result.Hits[0]["objectID"], "page 1 must return the second hit")
	require.Equal(t, 2, svc.lastLimit, "the backend must be asked for (page+1)*hitsPerPage hits")
}

// TestAlgoliaDocSearch_APIKeyHeaderAuthenticates pins the
// credential path the DocSearch client uses: the API key in the
// x-algolia-api-key header, not the Authorization header.
func TestAlgoliaDocSearch_APIKeyHeaderAuthenticates(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	cfg.Server.AuthTokens = []config.AuthToken{{Value: "s3cret", Label: "docsearch"}}
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := NewServer(cfg, svc)

	body := algoliaMultiBody(t, "install", url.Values{})

	t.Run("valid key is accepted", func(t *testing.T) {
		w := algoliaPost(t, s, "/1/indexes/*/queries", body, map[string]string{
			algoliaAPIKeyHeader: "s3cret",
		})
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	})

	t.Run("wrong key is rejected", func(t *testing.T) {
		w := algoliaPost(t, s, "/1/indexes/*/queries", body, map[string]string{
			algoliaAPIKeyHeader: "nope",
		})
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("missing key is rejected", func(t *testing.T) {
		w := algoliaPost(t, s, "/1/indexes/*/queries", body, nil)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// TestAlgoliaDocSearch_BackendFailureIsAnError pins that a broken
// backend is reported as a failure and not as an empty result: the
// DocSearch UI shows an error screen for a failed request and an
// empty-results screen for an empty answer.
func TestAlgoliaDocSearch_BackendFailureIsAnError(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchErr: errors.New("vector db down")}}
	s := algoliaNewServer(t, svc)

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "install", url.Values{}), nil)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	var resp struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(t, resp.Message, "vector db down")
}

// TestAlgoliaDocSearch_MalformedBodyIsABadRequest pins the 400 for
// a body the compat layer cannot parse.
func TestAlgoliaDocSearch_MalformedBodyIsABadRequest(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := algoliaNewServer(t, svc)

	w := algoliaPost(t, s, "/1/indexes/*/queries", `{"requests": "not-a-list"}`, nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAlgoliaDocSearch_IsRateLimited pins that the compat endpoint
// counts against the same per-IP bucket as /api/search: it runs the
// same hybrid search and carries the same embedding cost.
func TestAlgoliaDocSearch_IsRateLimited(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	cfg.Server.RateLimitPerMinute = 60
	cfg.Server.RateLimitBurst = 1
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := NewServer(cfg, svc)

	body := algoliaMultiBody(t, "install", url.Values{})
	post := func() int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/1/indexes/*/queries", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, http.StatusOK, post())
	require.Equal(t, http.StatusTooManyRequests, post(),
		"the compat endpoint must count against the LLM rate-limit bucket")
}

// TestAlgoliaDocSearch_APIKeyInQueryStringAuthenticates pins the
// authMode=withinQueryParameters variant of the Algolia client,
// which carries the key in the URL instead of the header.
func TestAlgoliaDocSearch_APIKeyInQueryStringAuthenticates(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	cfg.Server.AuthTokens = []config.AuthToken{{Value: "s3cret"}}
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := NewServer(cfg, svc)

	body := algoliaMultiBody(t, "install", url.Values{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/1/indexes/*/queries?x-algolia-api-key=s3cret", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// TestAlgoliaDocSearch_CSRFAllowsAPIKeyHeader pins that the
// DocSearch client is not blocked by the CSRF middleware: it
// carries a custom header, so the request is provably
// same-origin.
func TestAlgoliaDocSearch_CSRFAllowsAPIKeyHeader(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	cfg.Server.AuthTokens = []config.AuthToken{{Value: "s3cret"}}
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := NewServer(cfg, svc)

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "install", url.Values{}),
		map[string]string{algoliaAPIKeyHeader: "s3cret"})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// TestAlgoliaDocSearch_CORSAllowsAlgoliaHeaders pins that the
// browser preflight succeeds: the Algolia headers are custom
// headers, so they must be on the allow-list.
func TestAlgoliaDocSearch_CORSAllowsAlgoliaHeaders(t *testing.T) {
	chdirToRepoRoot(t)
	cfg := config.DefaultConfig()
	cfg.Server.EnableCORS = true
	cfg.Server.CORSOrigins = []string{"https://docs.example.com"}
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/1/indexes/*/queries", nil)
	req.Header.Set("Origin", "https://docs.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "x-algolia-api-key,x-algolia-application-id,x-algolia-agent")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "https://docs.example.com", w.Header().Get("Access-Control-Allow-Origin"))

	allowed := w.Header().Get("Access-Control-Allow-Headers")
	require.Contains(t, allowed, algoliaAPIKeyHeader)
	require.Contains(t, allowed, algoliaAppIDHeader)
	require.Contains(t, strings.ToLower(allowed), "x-algolia-agent")
}

// TestAlgoliaDocSearch_SingleIndexEndpoint pins the canonical
// Algolia single-index search endpoint.
func TestAlgoliaDocSearch_SingleIndexEndpoint(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := algoliaNewServer(t, svc)

	body := `{"query":"install","hitsPerPage":1,"attributesToRetrieve":["content","url"]}`
	w := algoliaPost(t, s, "/1/indexes/ragabast/query", body, nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Hits        []map[string]any `json:"hits"`
		NbHits      int              `json:"nbHits"`
		Query       string           `json:"query"`
		HitsPerPage int              `json:"hitsPerPage"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "install", resp.Query)
	require.Equal(t, 1, resp.HitsPerPage)
	require.Len(t, resp.Hits, 1)
	require.Equal(t, "chunk-1", resp.Hits[0]["objectID"])
}

// TestAlgoliaDocSearch_EmptyQueryReturnsEmptyPage pins the
// behavior for an empty query: Algolia would answer with the whole
// index, ragabast has no cheap "match everything" query, so the
// page is empty rather than a lie.
func TestAlgoliaDocSearch_EmptyQueryReturnsEmptyPage(t *testing.T) {
	svc := &algoliaFakeService{fakeHumaService: &fakeHumaService{searchResults: algoliaSampleResults()}}
	s := algoliaNewServer(t, svc)

	w := algoliaPost(t, s, "/1/indexes/*/queries", algoliaMultiBody(t, "", url.Values{}), nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Results []struct {
			Hits   []map[string]any `json:"hits"`
			NbHits int              `json:"nbHits"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Results, 1)
	require.Empty(t, resp.Results[0].Hits)
	require.Equal(t, 0, resp.Results[0].NbHits)
}

// TestAlgoliaHitURL_DropsUnsafeSchemes pins the URL guard: the
// DocSearch client renders the hit as a link, so a javascript: URL
// from an ingested document must not survive.
func TestAlgoliaHitURL_DropsUnsafeSchemes(t *testing.T) {
	require.Empty(t, algoliaHitURL(models.SearchResult{
		DocumentURLs: []string{"javascript:alert(1)"},
	}))
	require.Empty(t, algoliaHitURL(models.SearchResult{
		DocumentURLs: []string{"data:text/html,<script>alert(1)</script>"},
	}))
	require.Equal(t, "https://docs.example.com/x",
		algoliaHitURL(models.SearchResult{DocumentURLs: []string{"https://docs.example.com/x"}}))
	require.Equal(t, "https://docs.example.com/y",
		algoliaHitURL(models.SearchResult{CitationURL: "https://docs.example.com/y"}),
		"the citation URL wins over the raw document URLs")
}

// TestAlgoliaSnippet_UsesWindowAroundMatch pins the snippet
// window: a match in the middle of the text is surrounded by the
// requested number of words and the truncation is marked.
func TestAlgoliaSnippet_UsesWindowAroundMatch(t *testing.T) {
	text := "one two three four five six seven eight nine ten eleven twelve"

	got := algoliaSnippet(text, []string{"seven"}, 4, "<mark>", "</mark>", "…")
	require.Contains(t, got, "<mark>seven</mark>")
	require.True(t, strings.HasPrefix(got, "…"), "got %q", got)
	require.True(t, strings.HasSuffix(got, "…"), "got %q", got)

	// No match: the snippet starts at the beginning of the text.
	got = algoliaSnippet(text, []string{"nothing"}, 3, "<mark>", "</mark>", "…")
	require.Equal(t, "one two three…", got)
}
