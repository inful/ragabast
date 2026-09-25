// Package mcp exposes the ragabast corpus as Model Context
// Protocol (MCP) tools. AI agents that speak MCP
// (Claude Code, Claude Desktop, opencode, ...) can use
// ragabast as a knowledge source without bespoke HTTP
// plumbing.
//
// Two transports:
//
//   - Streamable HTTP: mounted at /mcp on the same
//     ragabast serve HTTP port. Bearer-token auth via
//     the existing server.auth_tokens. Opt-in via
//     server.mcp_http_enabled.
//
//   - Stdio: a separate subcommand (`ragabast mcp
//     serve --stdio`) for local operator workflows.
//     No auth — the operator IS the client.
//
// Tools exposed:
//
//   - search         wraps Service.HybridSearch
//   - query          wraps Service.QueryDebugWithOptions (stateless)
//   - list_documents wraps Service.ListDocumentsPaged
//   - get_document   wraps Service.GetDocument
package mcp

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// mcpSvc is the service subset the MCP tools need. The
// *service.Service satisfies it; tests substitute a
// stub. Keeping the interface in this package (not
// importing it from internal/web) avoids leaking MCP-
// specific shapes into the web layer.
type mcpSvc interface {
	HybridSearch(ctx context.Context, query string, limit int, filters service.SearchFilters, mode service.SearchMode) ([]models.SearchResult, error)
	QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error)
	ListDocumentsPaged(ctx context.Context, limit, offset int) ([]models.DocumentInfo, int, error)
	GetDocument(ctx context.Context, documentID string) (*models.Document, error)
}

// Server wraps the mcp-go server with ragabast-specific
// tool registration. Callers access it through
// (*Server).MCPServer to attach stdio or HTTP transports.
type Server struct {
	MCPServer *server.MCPServer
	svc       mcpSvc
	tools     []string // tracked locally — mcp-go doesn't expose the tool list publicly
}

// NewServer builds an MCP server with all four ragabast
// tools registered. The server is ready to be wired into
// either a stdio transport (`ragabast mcp serve --stdio`)
// or the HTTP mount at `/mcp` on the existing web server.
func NewServer(svc mcpSvc) *Server {
	mcpServer := server.NewMCPServer(
		"ragabast",
		"0.8.0",
		server.WithToolCapabilities(true),
	)
	s := &Server{MCPServer: mcpServer, svc: svc}
	s.registerTools()
	return s
}

// ToolNames returns the registered tool names in
// registration order. Exposed so tests can assert the
// public surface without going through the MCP
// protocol layer (the underlying mcp-go server doesn't
// expose its tool list).
func (s *Server) ToolNames() []string {
	out := make([]string, len(s.tools))
	copy(out, s.tools)
	return out
}

// HandleMessage drives a single JSON-RPC message through
// the underlying server. Exposed so tests can call
// tools without spinning up an actual transport.
func (s *Server) HandleMessage(ctx context.Context, raw json.RawMessage) mcp.JSONRPCMessage {
	return s.MCPServer.HandleMessage(ctx, raw)
}

// registerTools wires the four tool handlers into the
// MCP server. Each tool is registered via AddTool; the
// mcp-go framework auto-derives the JSON schema from
// the WithDescription / WithString / WithNumber
// options on the tool definition.
func (s *Server) registerTools() {
	s.tools = append(s.tools, "search")
	s.MCPServer.AddTool(
		mcp.NewTool("search",
			mcp.WithDescription("Hybrid search across the ragabast corpus. Returns up to `limit` ranked chunks with document title, similarity score, and source URL when docbuilder is configured."),
			mcp.WithString("query", mcp.Required(), mcp.Description("The search query.")),
			mcp.WithNumber("limit", mcp.Description("Maximum number of results. Defaults to 5, capped at 50.")),
			mcp.WithString("document_id", mcp.Description("Restrict to one document by id.")),
			mcp.WithString("tag", mcp.Description("Filter by frontmatter tag.")),
			mcp.WithString("category", mcp.Description("Filter by frontmatter category.")),
			mcp.WithString("mode", mcp.Description("Ranking strategy: hybrid (default), semantic, or keyword.")),
		),
		s.searchHandler,
	)

	s.tools = append(s.tools, "query")
	s.MCPServer.AddTool(
		mcp.NewTool("query",
			mcp.WithDescription("Chat-mode RAG. Sends the message to the LLM with retrieved context. Stateless — MCP clients manage conversation context themselves."),
			mcp.WithString("message", mcp.Required(), mcp.Description("The user's question.")),
			mcp.WithNumber("limit", mcp.Description("Maximum number of context chunks to retrieve. Defaults to 5.")),
		),
		s.queryHandler,
	)

	s.tools = append(s.tools, "list_documents")
	s.MCPServer.AddTool(
		mcp.NewTool("list_documents",
			mcp.WithDescription("List ingested documents, paginated. Default page size 25, capped at 1000."),
			mcp.WithNumber("limit", mcp.Description("Page size. Defaults to 25.")),
			mcp.WithNumber("offset", mcp.Description("Zero-based page offset. Defaults to 0.")),
		),
		s.listDocumentsHandler,
	)

	s.tools = append(s.tools, "get_document")
	s.MCPServer.AddTool(
		mcp.NewTool("get_document",
			mcp.WithDescription("Fetch one document by id, including chunk count, tags, and categories."),
			mcp.WithString("document_id", mcp.Required(), mcp.Description("The document id.")),
		),
		s.getDocumentHandler,
	)
}
