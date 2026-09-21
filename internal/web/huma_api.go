package web

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/web/jobs"
)

// RegisterHumaOperations registers every Huma API operation on
// api. The actual handlers live in the per-resource files:
//
//	huma_catalog.go     GET  /api/health, /api/tags, /api/categories, /api/tags-categories
//	huma_query.go       POST /api/query, /api/search
//	huma_ingest.go      POST /api/ingest, /api/ingest/raw, /api/ingest/file
//	huma_documents.go   GET  /api/documents, DELETE /api/documents/{id}, POST /api/documents/prune
//	huma_links.go       POST /api/link-suggestions
//	huma_frontmatter.go POST /api/frontmatter/suggest
//
// Cross-resource helpers (canonicalizeCategories, filterAllowed,
// splitAllowedAndCustomTags, normalizeStringSlice, uniqueAppend,
// uniqueNonEmptyStrings, normalizeTagsLower, planPrune*) live in
// huma_helpers.go; the link-extraction helper lives in
// huma_common.go.
//
// The signature MUST stay stable — huma_api_test.go and the chi
// adapter in huma_chi.go both call this exact function.
func registerHumaOperations(router http.Handler, api huma.API, svc serviceAPI, limiter *IngestLimiter, maxIngestDocumentBytes int, ingestQueue *jobs.Queue) {
	registerCatalogOperations(api, svc, ingestQueue)
	registerQueryOperation(api, svc)
	registerSearchOperation(api, svc)
	registerIngestOperations(api, svc, limiter, maxIngestDocumentBytes)
	registerDocumentsOperations(api, svc)
	registerLinkSuggestionsOperation(api, svc)
	registerFrontmatterOperation(api, svc)
	registerIngestJobsOperations(api, router, ingestQueue)
}
