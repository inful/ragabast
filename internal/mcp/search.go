package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// searchHandler implements the `search` tool. It threads
// the optional filters and mode parameter through to
// HybridSearch and formats the results as a markdown
// bullet list so AI agents can parse them easily.
//
// Format:
//
//   - **<title>** (`<doc_id>`, similarity <score>)
//     <url>
//     <chunk snippet...>
//
// Markdown over JSON because:
//
//  1. Models read markdown natively — no parsing step.
//  2. The agent can cite by chunk title without parsing.
//  3. Future: when source citations land in the chat
//     transcript export (issue #76), this format
//     matches.
func (s *Server) searchHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := req.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	limit := int(req.GetFloat("limit", 5))
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		// Cap at 50 — anything larger is almost certainly
		// an agent misuse (or a DoS against the embeddings
		// server via the keyword side, which is local).
		limit = 50
	}

	filters := service.SearchFilters{
		DocumentID: req.GetString("document_id", ""),
		Tag:        req.GetString("tag", ""),
		Category:   req.GetString("category", ""),
	}

	mode := service.ModeHybrid
	switch strings.ToLower(req.GetString("mode", "")) {
	case "semantic":
		mode = service.ModeSemantic
	case "keyword":
		mode = service.ModeKeyword
	case "", "hybrid":
		// keep ModeHybrid
	default:
		return mcp.NewToolResultError(
			fmt.Sprintf("invalid mode %q (want hybrid, semantic, or keyword)",
				req.GetString("mode", ""))), nil
	}

	results, err := s.svc.HybridSearch(ctx, query, limit, filters, mode)
	if err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("search failed: %v", err)), nil
	}

	return mcp.NewToolResultText(formatSearchResults(results)), nil
}

// formatSearchResults renders the result slice as
// markdown. Returns an empty-string message for an
// empty result set rather than an error — agents can
// distinguish "no results" from "tool failed" via the
// result type, and an empty result is a valid outcome
// ("I searched and found nothing").
func formatSearchResults(results []models.SearchResult) string {
	if len(results) == 0 {
		return "No results."
	}
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. **%s** (`%s`, similarity %.2f)\n",
			i+1, r.DocumentTitle, r.DocumentID, r.Similarity)
		if r.DocbuilderURL != "" {
			fmt.Fprintf(&b, "   %s\n", r.DocbuilderURL)
		}
		b.WriteString("\n")
	}
	return b.String()
}
