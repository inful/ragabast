package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/vector"
)

type documentsResponseBody struct {
	Documents []models.DocumentInfo `json:"documents"`
	Total     int                   `json:"total"`
	Limit     int                   `json:"limit"`
	Offset    int                   `json:"offset"`
}

type deleteDocumentResponseBody struct {
	Message    string `json:"message"`
	DocumentID string `json:"document_id"`
}

// pruneDocumentsRequestBody drives POST /api/documents/prune.
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

// bulkUpdatePatch is one entry in a bulk-update POST body.
// Mirrors vector.DocumentMetadataPatch but with a
// huma-shaped document_id field that drives the schema
// validation. All metadata fields are optional — an empty
// patch acts as a "touch" that bumps document_updated_at
// without changing content (operators use this to force
// cache invalidation downstream).
type bulkUpdatePatch struct {
	DocumentID string   `doc:"Document to update; required." json:"document_id" required:"true"`
	Tags       []string `doc:"New tags; nil = no change, empty = clear." json:"tags,omitempty"`
	Categories []string `doc:"New categories; nil = no change." json:"categories,omitempty"`
	URLs       []string `doc:"New URLs; nil = no change." json:"urls,omitempty"`
}

// bulkUpdateRequestBody drives POST /api/documents/bulk-update.
type bulkUpdateRequestBody struct {
	Patches []bulkUpdatePatch `doc:"One patch per document to update." json:"patches" min:"1" required:"true"`
	Mode    string            `doc:"Tag semantics: merge (default) keeps existing tags and adds new ones; replace overwrites wholesale. Categories and URLs always replace wholesale." enum:"merge,replace" json:"mode,omitempty"`
}

// bulkUpdateItemResponse is one element of the bulk-update
// response array. items[i] corresponds to the i-th patch
// in the request body, in input order.
type bulkUpdateItemResponse struct {
	DocumentID string `json:"document_id"`
	Status     string `doc:"ok on success, error on failure." json:"status"`
	Error      string `json:"error,omitempty"`
}

// bulkUpdateResponseBody is the envelope returned from
// POST /api/documents/bulk-update.
type bulkUpdateResponseBody struct {
	Items []bulkUpdateItemResponse `json:"items"`
}

// registerDocumentsOperations wires GET /api/documents,
// DELETE /api/documents/{id}, and POST /api/documents/prune.
func registerDocumentsOperations(api huma.API, svc serviceAPI) {
	huma.Register(api, huma.Operation{
		OperationID: "documents",
		Method:      http.MethodGet,
		Path:        "/api/documents",
		Summary:     "List ingested documents",
		Description: "Returns up to `limit` documents (default 25, max 1000) starting at `offset`, sorted by document ID for stable pagination. The `total` field reflects the count of all matching documents, not just the page.",
	}, func(ctx context.Context, input *struct {
		// No minimum/maximum constraints on the query
		// params: clamping is the handler's job (see
		// below). Rejecting at the schema layer would
		// surface as a 400, but the spec is to clamp
		// silently — operators asking for limit=10000
		// get the cap, not a rejection.
		Limit  int `doc:"Page size; default 25, capped at 1000." query:"limit,omitempty"`
		Offset int `doc:"Zero-based page offset; clamped to [0, total]." query:"offset,omitempty"`
	},
	) (*struct{ Body documentsResponseBody }, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 25
		}
		docs, total, err := svc.ListDocumentsPaged(ctx, limit, input.Offset)
		if err != nil {
			return nil, huma.Error500InternalServerError("list documents failed")
		}
		return &struct{ Body documentsResponseBody }{Body: documentsResponseBody{
			Documents: docs,
			Total:     total,
			Limit:     limit,
			Offset:    input.Offset,
		}}, nil
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

		// Issue #9 (N+1 fix): the previous implementation
		// called svc.ListDocuments(ctx) just to check
		// existence, then called DeleteDocument. That made
		// every delete a full-collection scan. The vector
		// layer now returns vector.ErrNotFound when no
		// chunks match the document_id, so the pre-check
		// is redundant — one call does both jobs.
		if err := svc.DeleteDocument(ctx, documentID); err != nil {
			if errors.Is(err, vector.ErrNotFound) {
				return nil, huma.Error404NotFound("document not found")
			}
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

	huma.Register(api, huma.Operation{
		OperationID: "bulk-update-documents",
		Method:      http.MethodPost,
		Path:        "/api/documents/bulk-update",
		Summary:     "Bulk-update document metadata (tags, categories, URLs)",
		Description: "Patches metadata on each document in the patches array. Tag semantics: merge (default) keeps existing tags and adds new ones, deduped; replace overwrites wholesale. Categories and URLs always replace wholesale when the patch sets them. Per-document failures (missing document, partial write) are reported as per-item errors without aborting the rest of the batch. Returns 200 with an items array; each item has status=ok or status=error.",
	}, func(ctx context.Context, input *struct{ Body bulkUpdateRequestBody }) (*struct{ Body bulkUpdateResponseBody }, error) {
		body := input.Body

		// Translate the huma-shaped patches into the
		// vector-layer DocumentMetadataPatch. We do the
		// translation in the handler so the huma struct
		// stays decoupled from the vector struct (and can
		// add fields like \`mode\` without forcing a
		// vector-layer change).
		patches := make([]vector.DocumentMetadataPatch, len(body.Patches))
		for i, p := range body.Patches {
			patches[i] = vector.DocumentMetadataPatch{
				DocumentID: strings.TrimSpace(p.DocumentID),
				Tags:       p.Tags,
				Categories: p.Categories,
				URLs:       p.URLs,
			}
		}

		results, err := svc.BulkUpdateDocuments(ctx, patches, body.Mode)
		if err != nil {
			if errors.Is(err, models.ErrInvalidInput) {
				return nil, huma.Error400BadRequest("patches must be non-empty")
			}
			return nil, huma.Error500InternalServerError("bulk update failed: " + err.Error())
		}

		items := make([]bulkUpdateItemResponse, len(results))
		for i, r := range results {
			items[i] = bulkUpdateItemResponse{
				DocumentID: r.DocumentID,
				Status:     r.Status,
				Error:      r.Error,
			}
		}
		return &struct{ Body bulkUpdateResponseBody }{Body: bulkUpdateResponseBody{Items: items}}, nil
	})
}
