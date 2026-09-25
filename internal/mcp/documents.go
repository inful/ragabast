package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// listDocumentsHandler implements the `list_documents`
// tool. Mirrors the HTTP API's pagination contract —
// default page size 25, capped at 1000.
func (s *Server) listDocumentsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := int(req.GetFloat("limit", 25))
	if limit <= 0 {
		limit = 25
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := max(int(req.GetFloat("offset", 0)), 0)

	docs, total, err := s.svc.ListDocumentsPaged(ctx, limit, offset)
	if err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("list documents failed: %v", err)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Showing %d–%d of %d documents\n\n",
		offset+1, offset+len(docs), total)
	for _, d := range docs {
		fmt.Fprintf(&b, "- **%s** (`%s`)\n",
			d.Title, d.ID)
	}
	if total > offset+len(docs) {
		fmt.Fprintf(&b, "\n_Next page: offset=%d_\n", offset+limit)
	}
	return mcp.NewToolResultText(b.String()), nil
}

// getDocumentHandler implements the `get_document` tool.
// Returns the document with chunk metadata; missing
// document ids surface as MCP errors (not silent
// empties).
func (s *Server) getDocumentHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	docID := req.GetString("document_id", "")
	if docID == "" {
		return mcp.NewToolResultError("document_id is required"), nil
	}

	doc, err := s.svc.GetDocument(ctx, docID)
	if err != nil {
		// "not found" is a normal outcome — the client
		// asked for something that doesn't exist. Surface
		// it as a tool error so the agent can branch on
		// the outcome ("document doesn't exist") rather
		// than seeing an empty body and assuming success.
		if errors.Is(err, errNotFoundMarker) || strings.Contains(err.Error(), "not found") {
			return mcp.NewToolResultError(
				fmt.Sprintf("document %q not found", docID)), nil
		}
		return mcp.NewToolResultError(
			fmt.Sprintf("get document failed: %v", err)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (`%s`)\n", doc.Title, doc.ID)
	fmt.Fprintf(&b, "\nChunks: %d\n", len(doc.Chunks))
	if len(doc.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n", strings.Join(doc.Tags, ", "))
	}
	if len(doc.Categories) > 0 {
		fmt.Fprintf(&b, "Categories: %s\n", strings.Join(doc.Categories, ", "))
	}
	if len(doc.URLs) > 0 {
		fmt.Fprintf(&b, "URLs: %s\n", strings.Join(doc.URLs, ", "))
	}
	return mcp.NewToolResultText(b.String()), nil
}

// errNotFoundMarker is reserved for future use when the
// service layer grows typed errors. For now, the
// document lookup returns plain strings; the handler
// falls back to substring matching on "not found".
var errNotFoundMarker = errors.New("not found")
