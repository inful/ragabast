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
}
