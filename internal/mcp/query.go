package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ragabast/internal/service"
)

// queryHandler implements the `query` tool. It's
// stateless — the chat session store is keyed by cookie
// and MCP clients don't have cookies. MCP clients that
// want conversation context should manage it
// themselves (the `message` argument is the full turn).
//
// Returns the LLM answer plus the source list in
// markdown form, mirroring the search tool's format so
// agents can parse both with the same logic.
func (s *Server) queryHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	message := req.GetString("message", "")
	if message == "" {
		return mcp.NewToolResultError("message is required"), nil
	}

	limit := int(req.GetFloat("limit", 5))
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}

	// Empty LLMOptions — no History threading. The MCP
	// client owns the conversation context; the chat
	// session store is intentionally bypassed.
	answer, info, err := s.svc.QueryDebugWithOptions(ctx, message, limit, service.LLMOptions{})
	if err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("query failed: %v", err)), nil
	}

	var b strings.Builder
	b.WriteString(answer)
	b.WriteString("\n\n")
	if info != nil && len(info.Results) > 0 {
		b.WriteString("**Sources**\n\n")
		b.WriteString(formatSearchResults(info.Results))
	}
	return mcp.NewToolResultText(b.String()), nil
}
