package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"gopkg.in/yaml.v3"
)

type queryRequestBody struct {
	Query       string   `doc:"Natural language query" json:"query"`
	TopK        int      `default:"5" doc:"Number of results to consider" json:"top_k" minimum:"1"`
	Temperature *float64 `doc:"LLM temperature (sampling). If omitted, uses model default." json:"temperature,omitempty"`
	IncludeHits bool     `doc:"Include retrieved chunks in the response." json:"include_hits,omitempty"`
	History     []struct {
		Role    string `doc:"Message role (user|assistant)." json:"role"`
		Content string `doc:"Message content." json:"content"`
	} `doc:"Optional conversation history for follow-up questions." json:"history,omitempty"`
}

type queryResponseBody struct {
	Answer string     `json:"answer"`
	Links  []string   `json:"links,omitempty"`
	Hits   []queryHit `json:"hits,omitempty"`
}

type queryHit struct {
	DocumentTitle string   `json:"document_title"`
	Content       string   `json:"content"`
	Similarity    float32  `json:"similarity"`
	DocumentURLs  []string `json:"document_urls,omitempty"`
}

type ingestRequestBody struct {
	Content string `doc:"Docubilder document content (including YAML frontmatter)." json:"content"`
}

type ingestResponseBody struct {
	Message    string `json:"message"`
	DocumentID string `json:"document_id"`
	Chunks     int    `json:"chunks"`
}

type healthResponseBody struct {
	Status string `json:"status"`
}

type searchRequestBody struct {
	Query    string  `doc:"Search query" json:"query"`
	Limit    int     `default:"5" doc:"Number of results" json:"limit" minimum:"1"`
	MinScore float64 `default:"0.5" doc:"Minimum similarity score" json:"min_score" maximum:"1" minimum:"0"`
}

type searchResultBody struct {
	ChunkID       string   `json:"chunk_id"`
	DocumentID    string   `json:"document_id"`
	DocumentTitle string   `json:"document_title"`
	Content       string   `json:"content"`
	Similarity    float32  `json:"similarity"`
	DocumentURLs  []string `json:"document_urls,omitempty"`
}

type searchResponseBody struct {
	Count   int                `json:"count"`
	Results []searchResultBody `json:"results"`
}

type documentsResponseBody struct {
	Documents []models.DocumentInfo `json:"documents"`
}

type deleteDocumentResponseBody struct {
	Message    string `json:"message"`
	DocumentID string `json:"document_id"`
}

type pruneDocumentsRequestBody struct {
	KeepDocumentIDs   []string `doc:"Keep only these document IDs (delete the rest)." json:"keep_document_ids,omitempty"`
	KeepUIDs          []string `doc:"Keep only these UIDs (delete the rest)." json:"keep_uids,omitempty"`
	DeleteDocumentIDs []string `doc:"Explicit list of document IDs to delete." json:"delete_document_ids,omitempty"`
	DryRun            bool     `doc:"If true, compute the prune plan but don't delete anything." json:"dry_run,omitempty"`
}

type pruneDocumentsResponseBody struct {
	DryRun   bool     `json:"dry_run"`
	Deleted  []string `json:"deleted_document_ids"`
	Kept     []string `json:"kept_document_ids,omitempty"`
	NotFound []string `json:"not_found_document_ids,omitempty"`
}

type linkSuggestionsRequestBody struct {
	Text string `doc:"A section of documentation used to find related documents" json:"text"`

	TopK     *int     `default:"10" doc:"Number of search hits to consider" json:"top_k,omitempty" minimum:"1"`
	MaxURLs  *int     `default:"10" doc:"Maximum number of URLs to return" json:"max_urls,omitempty" minimum:"1"`
	MinScore *float64 `default:"0.5" doc:"Minimum similarity score" json:"min_score,omitempty" maximum:"1" minimum:"0"`
}

type linkSuggestionsResponseBody struct {
	URLs []string `json:"urls"`
}

type frontmatterSuggestRequestBody struct {
	Content           string   `doc:"Document content (may include YAML frontmatter)." json:"content"`
	AllowedCategories []string `doc:"Allowed categories list (suggestions must come from here)." json:"allowed_categories"`
	AllowedTags       []string `doc:"Preferred tags list (suggestions should prefer these)." json:"allowed_tags"`
}

type frontmatterSuggestResponseBody struct {
	Frontmatter map[string]any `json:"frontmatter"`
	Applied     map[string]any `json:"applied"`
}

func splitDocubilderFrontmatter(raw string) (frontmatterYAML []byte, markdown string, ok bool) {
	content := strings.TrimSpace(raw)
	if !strings.HasPrefix(content, "---\n") {
		return nil, content, false
	}
	rest := content[len("---\n"):]
	before, after, ok0 := strings.Cut(rest, "\n---\n")
	if !ok0 {
		return nil, content, false
	}
	fm := strings.TrimSpace(before)
	md := strings.TrimSpace(after)
	return []byte(fm), md, true
}

func normalizeStringSlice(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func uniqueAppend(dst []string, src []string) []string {
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, v := range dst {
		seen[v] = struct{}{}
	}
	for _, v := range src {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		dst = append(dst, v)
	}
	return dst
}

func filterAllowed(values []string, allowed []string) []string {
	allow := make(map[string]string, len(allowed))
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		allow[strings.ToLower(a)] = a
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		canonical, ok := allow[strings.ToLower(v)]
		if !ok {
			continue
		}
		out = append(out, canonical)
	}
	return uniqueNonEmptyStrings(out)
}

func splitAllowedAndCustomTags(suggested []string, allowed []string) (allowedTags []string, customTags []string) {
	allow := make(map[string]string, len(allowed))
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		allow[strings.ToLower(a)] = a
	}
	for _, v := range uniqueNonEmptyStrings(suggested) {
		canonical, ok := allow[strings.ToLower(v)]
		if ok {
			allowedTags = append(allowedTags, canonical)
			continue
		}
		customTags = append(customTags, v)
	}
	return uniqueNonEmptyStrings(allowedTags), uniqueNonEmptyStrings(customTags)
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		norm := strings.TrimSpace(v)
		if norm == "" {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	return out
}

func planPruneByExplicitDelete(docsByID map[string]models.DocumentInfo, deleteIDs []string) (toDelete []string, notFound []string) {
	ids := uniqueNonEmptyStrings(deleteIDs)
	toDelete = make([]string, 0, len(ids))
	notFound = make([]string, 0, 4)
	for _, documentID := range ids {
		if _, ok := docsByID[documentID]; !ok {
			notFound = append(notFound, documentID)
			continue
		}
		toDelete = append(toDelete, documentID)
	}
	return toDelete, notFound
}

func planPruneByKeepList(docs []models.DocumentInfo, keepDocumentIDs []string, keepUIDs []string) (toDelete []string, kept []string) {
	keepIDs := make(map[string]struct{}, len(keepDocumentIDs))
	for _, documentID := range uniqueNonEmptyStrings(keepDocumentIDs) {
		keepIDs[documentID] = struct{}{}
	}
	keepByUID := make(map[string]struct{}, len(keepUIDs))
	for _, uid := range uniqueNonEmptyStrings(keepUIDs) {
		keepByUID[uid] = struct{}{}
	}

	toDelete = make([]string, 0, len(docs))
	kept = make([]string, 0, len(docs))
	for _, d := range docs {
		_, keepByID := keepIDs[d.ID]
		_, keepUID := keepByUID[d.UID]
		if keepByID || keepUID {
			kept = append(kept, d.ID)
			continue
		}
		toDelete = append(toDelete, d.ID)
	}

	return toDelete, kept
}

func RegisterHumaOperations(api huma.API, svc serviceAPI, limiter *IngestLimiter) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
		Summary:     "Health check",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body healthResponseBody }, error) {
		_, err := svc.CheckHealth(ctx)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("Service unhealthy")
		}
		return &struct{ Body healthResponseBody }{Body: healthResponseBody{Status: "healthy"}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest",
		Method:      http.MethodPost,
		Path:        "/api/ingest",
		Summary:     "Ingest a docubilder document",
	}, func(ctx context.Context, input *struct{ Body ingestRequestBody }) (*struct{ Body ingestResponseBody }, error) {
		if limiter != nil {
			if !limiter.TryAcquire() {
				return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests("ingest busy"), limiter.RetryAfterHeader())
			}
			defer limiter.Release()
		}

		content := strings.TrimSpace(input.Body.Content)
		if content == "" {
			return nil, huma.Error400BadRequest("content is required")
		}

		doc, err := svc.IngestDocument(ctx, content)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-raw",
		Method:      http.MethodPost,
		Path:        "/api/ingest/raw",
		Summary:     "Ingest a docubilder markdown document (raw body)",
	}, func(ctx context.Context, input *struct {
		RawBody []byte `contentType:"text/markdown" required:"true"`
	},
	) (*struct{ Body ingestResponseBody }, error) {
		if limiter != nil {
			if !limiter.TryAcquire() {
				return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests("ingest busy"), limiter.RetryAfterHeader())
			}
			defer limiter.Release()
		}

		if len(bytes.TrimSpace(input.RawBody)) == 0 {
			return nil, huma.Error400BadRequest("request body is required")
		}
		content := string(input.RawBody)

		doc, err := svc.IngestDocument(ctx, content)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "ingest-file",
		Method:      http.MethodPost,
		Path:        "/api/ingest/file",
		Summary:     "Ingest a docubilder markdown document (multipart upload)",
	}, func(ctx context.Context, input *struct {
		RawBody huma.MultipartFormFiles[struct {
			File huma.FormFile `form:"file" required:"true"`
		}]
	},
	) (*struct{ Body ingestResponseBody }, error) {
		if limiter != nil {
			if !limiter.TryAcquire() {
				return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests("ingest busy"), limiter.RetryAfterHeader())
			}
			defer limiter.Release()
		}

		form := input.RawBody.Data()
		b, err := io.ReadAll(form.File)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to read uploaded file")
		}

		if len(bytes.TrimSpace(b)) == 0 {
			return nil, huma.Error400BadRequest("file is empty")
		}
		content := string(b)

		doc, err := svc.IngestDocument(ctx, content)
		if err != nil {
			return nil, huma.Error400BadRequest("failed to ingest")
		}

		return &struct{ Body ingestResponseBody }{Body: ingestResponseBody{
			Message:    "Document ingested successfully",
			DocumentID: doc.ID,
			Chunks:     len(doc.Chunks),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "query",
		Method:      http.MethodPost,
		Path:        "/api/query",
		Summary:     "Query the knowledge base",
	}, func(ctx context.Context, input *struct{ Body queryRequestBody }) (*struct{ Body queryResponseBody }, error) {
		q := strings.TrimSpace(input.Body.Query)
		if q == "" {
			return nil, huma.Error400BadRequest("query is required")
		}

		topK := input.Body.TopK
		if topK == 0 {
			topK = 5
		}

		history := make([]service.ChatMessage, 0, len(input.Body.History))
		for _, m := range input.Body.History {
			history = append(history, service.ChatMessage{Role: m.Role, Content: m.Content})
		}

		answer, debug, err := svc.QueryDebugWithOptions(ctx, q, topK, service.LLMOptions{Temperature: input.Body.Temperature, History: history})
		if err != nil {
			return nil, huma.Error500InternalServerError("query failed")
		}

		resp := queryResponseBody{Answer: answer}
		if debug != nil {
			resp.Links = extractLinksFromResults(debug.Results)
			if input.Body.IncludeHits {
				resp.Hits = make([]queryHit, 0, len(debug.Results))
				for _, r := range debug.Results {
					resp.Hits = append(resp.Hits, queryHit{
						DocumentTitle: r.DocumentTitle,
						Content:       r.Content,
						Similarity:    r.Similarity,
						DocumentURLs:  r.DocumentURLs,
					})
				}
			}
		}

		return &struct{ Body queryResponseBody }{Body: resp}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "search",
		Method:      http.MethodPost,
		Path:        "/api/search",
		Summary:     "Semantic search",
	}, func(ctx context.Context, input *struct{ Body searchRequestBody }) (*struct{ Body searchResponseBody }, error) {
		q := strings.TrimSpace(input.Body.Query)
		if q == "" {
			return nil, huma.Error400BadRequest("query is required")
		}

		limit := input.Body.Limit
		if limit == 0 {
			limit = 5
		}
		minScore := input.Body.MinScore
		if minScore == 0 {
			minScore = 0.5
		}

		results, err := svc.Search(ctx, q, limit, nil)
		if err != nil {
			return nil, huma.Error500InternalServerError("search failed")
		}

		filtered := make([]searchResultBody, 0, len(results))
		for _, r := range results {
			if float64(r.Similarity) < minScore {
				continue
			}
			filtered = append(filtered, searchResultBody{
				ChunkID:       r.ChunkID,
				DocumentID:    r.DocumentID,
				DocumentTitle: r.DocumentTitle,
				Content:       r.Content,
				Similarity:    r.Similarity,
				DocumentURLs:  r.DocumentURLs,
			})
		}

		return &struct{ Body searchResponseBody }{Body: searchResponseBody{Count: len(filtered), Results: filtered}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "link-suggestions",
		Method:      http.MethodPost,
		Path:        "/api/link-suggestions",
		Summary:     "Suggest related document links",
		Description: "Given a section of documentation, search the vector database and return a set of related document URLs.",
	}, func(ctx context.Context, input *struct{ Body linkSuggestionsRequestBody }) (*struct{ Body linkSuggestionsResponseBody }, error) {
		text := strings.TrimSpace(input.Body.Text)
		if text == "" {
			return nil, huma.Error400BadRequest("text is required")
		}

		topK := 10
		if input.Body.TopK != nil {
			topK = *input.Body.TopK
		}
		if topK == 0 {
			topK = 10
		}
		maxURLs := 10
		if input.Body.MaxURLs != nil {
			maxURLs = *input.Body.MaxURLs
		}
		if maxURLs == 0 {
			maxURLs = 10
		}
		minScore := 0.5
		if input.Body.MinScore != nil {
			minScore = *input.Body.MinScore
		}
		if minScore == 0 {
			minScore = 0.5
		}

		results, err := svc.Search(ctx, text, topK, nil)
		if err != nil {
			return nil, huma.Error500InternalServerError("link suggestions search failed")
		}

		filtered := make([]models.SearchResult, 0, len(results))
		for _, r := range results {
			if float64(r.Similarity) < minScore {
				continue
			}
			filtered = append(filtered, r)
		}

		urls := extractLinksFromResults(filtered)
		if len(urls) > maxURLs {
			urls = urls[:maxURLs]
		}

		return &struct{ Body linkSuggestionsResponseBody }{Body: linkSuggestionsResponseBody{URLs: urls}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "documents",
		Method:      http.MethodGet,
		Path:        "/api/documents",
		Summary:     "List ingested documents",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body documentsResponseBody }, error) {
		docs, err := svc.ListDocuments(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list documents failed")
		}
		return &struct{ Body documentsResponseBody }{Body: documentsResponseBody{Documents: docs}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "frontmatter-suggest",
		Method:      http.MethodPost,
		Path:        "/api/frontmatter/suggest",
		Summary:     "Suggest frontmatter fields",
		Description: "Uses the LLM to propose description, tags and categories from the document content. This endpoint does NOT retrieve vector DB context.",
	}, func(ctx context.Context, input *struct{ Body frontmatterSuggestRequestBody }) (*struct {
		Body frontmatterSuggestResponseBody
	}, error,
	) {
		content := strings.TrimSpace(input.Body.Content)
		if content == "" {
			return nil, huma.Error400BadRequest("content is required")
		}
		if len(input.Body.AllowedCategories) == 0 {
			return nil, huma.Error400BadRequest("allowed_categories is required")
		}
		if len(input.Body.AllowedTags) == 0 {
			return nil, huma.Error400BadRequest("allowed_tags is required")
		}

		fmBytes, markdown, hasFM := splitDocubilderFrontmatter(content)
		existing := map[string]any{}
		if hasFM && len(fmBytes) > 0 {
			if err := yaml.Unmarshal(fmBytes, &existing); err != nil {
				return nil, huma.Error400BadRequest("invalid YAML frontmatter")
			}
		}

		sug, err := svc.SuggestFrontmatter(ctx, markdown, existing, input.Body.AllowedCategories, input.Body.AllowedTags)
		if err != nil {
			return nil, huma.Error500InternalServerError("frontmatter suggestion failed")
		}

		applied := map[string]any{}
		merged := make(map[string]any, len(existing)+3)
		maps.Copy(merged, existing)

		// Description: only set if empty/missing.
		if cur, ok := merged["description"].(string); ok && strings.TrimSpace(cur) != "" {
			applied["description_set"] = false
		} else if strings.TrimSpace(sug.Description) != "" {
			merged["description"] = strings.TrimSpace(sug.Description)
			applied["description_set"] = true
		} else {
			applied["description_set"] = false
		}

		// Categories: keep existing, add allowed suggestions.
		existingCats := []string{}
		if raw, ok := merged["categories"].([]any); ok {
			existingCats = normalizeStringSlice(raw)
		}
		addedCats := filterAllowed(sug.Categories, input.Body.AllowedCategories)
		finalCats := uniqueAppend(existingCats, addedCats)
		if len(finalCats) > 0 {
			merged["categories"] = finalCats
		}
		applied["categories_added"] = addedCats

		// Tags: keep existing, add allowed tags + custom tags.
		existingTags := []string{}
		if raw, ok := merged["tags"].([]any); ok {
			existingTags = normalizeStringSlice(raw)
		}
		addedAllowedTags, customFromTags := splitAllowedAndCustomTags(sug.Tags, input.Body.AllowedTags)
		customTags := uniqueAppend(customFromTags, uniqueNonEmptyStrings(sug.CustomTags))
		finalTags := uniqueAppend(existingTags, uniqueAppend(addedAllowedTags, customTags))
		if len(finalTags) > 0 {
			merged["tags"] = finalTags
		}
		applied["tags_added"] = addedAllowedTags
		applied["custom_tags_added"] = customTags

		// Ensure response JSON is stable (no yaml.Node etc).
		_, _ = json.Marshal(merged)

		return &struct {
			Body frontmatterSuggestResponseBody
		}{Body: frontmatterSuggestResponseBody{Frontmatter: merged, Applied: applied}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-document",
		Method:      http.MethodDelete,
		Path:        "/api/documents/{document_id}",
		Summary:     "Delete an ingested document",
		Description: "Deletes all stored chunks for the document.",
	}, func(ctx context.Context, input *struct {
		DocumentID string `path:"document_id"`
	},
	) (*struct{ Body deleteDocumentResponseBody }, error) {
		documentID := strings.TrimSpace(input.DocumentID)
		if documentID == "" {
			return nil, huma.Error400BadRequest("document_id is required")
		}

		docs, err := svc.ListDocuments(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list documents failed")
		}
		found := false
		for _, d := range docs {
			if d.ID == documentID {
				found = true
				break
			}
		}
		if !found {
			return nil, huma.Error404NotFound("document not found")
		}

		if err := svc.DeleteDocument(ctx, documentID); err != nil {
			return nil, huma.Error500InternalServerError("delete document failed")
		}

		return &struct{ Body deleteDocumentResponseBody }{Body: deleteDocumentResponseBody{Message: "Document deleted", DocumentID: documentID}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "prune-documents",
		Method:      http.MethodPost,
		Path:        "/api/documents/prune",
		Summary:     "Prune ingested documents",
		Description: "Deletes documents according to an explicit delete list or a keep list. Useful for syncing the DB to an external source of truth.",
	}, func(ctx context.Context, input *struct{ Body pruneDocumentsRequestBody }) (*struct{ Body pruneDocumentsResponseBody }, error) {
		body := input.Body

		hasDeleteList := len(body.DeleteDocumentIDs) > 0
		hasKeepList := len(body.KeepDocumentIDs) > 0 || len(body.KeepUIDs) > 0
		if hasDeleteList && hasKeepList {
			return nil, huma.Error400BadRequest("provide either delete_document_ids or a keep list, not both")
		}
		if !hasDeleteList && !hasKeepList {
			return nil, huma.Error400BadRequest("provide delete_document_ids, keep_document_ids, or keep_uids")
		}

		docs, err := svc.ListDocuments(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list documents failed")
		}

		docsByID := make(map[string]models.DocumentInfo, len(docs))
		for _, d := range docs {
			docsByID[d.ID] = d
		}

		var toDelete []string
		var kept []string
		var notFound []string
		if hasDeleteList {
			toDelete, notFound = planPruneByExplicitDelete(docsByID, body.DeleteDocumentIDs)
		} else {
			toDelete, kept = planPruneByKeepList(docs, body.KeepDocumentIDs, body.KeepUIDs)
		}

		if !body.DryRun {
			for _, documentID := range toDelete {
				if err := svc.DeleteDocument(ctx, documentID); err != nil {
					return nil, huma.Error500InternalServerError("prune failed")
				}
			}
		}

		resp := pruneDocumentsResponseBody{DryRun: body.DryRun, Deleted: toDelete, Kept: kept, NotFound: notFound}
		return &struct{ Body pruneDocumentsResponseBody }{Body: resp}, nil
	})
}

func extractLinksFromResults(results []models.SearchResult) []string {
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, r := range results {
		for _, u := range r.DocumentURLs {
			url := strings.TrimSpace(u)
			if url == "" {
				continue
			}
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			out = append(out, url)
		}
	}
	return out
}
