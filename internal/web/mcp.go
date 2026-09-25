package web

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/server"
	"github.com/ragabast/internal/mcp"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// mcpHTTPHandler builds the streamable-HTTP MCP handler
// to mount at /mcp on the existing chi router.
//
// Authentication is inherited from the global
// authMiddleware — the existing server.auth_tokens list
// applies. The csrfMiddleware already exempts requests
// with an Authorization header, so bearer-auth MCP
// clients don't need a CSRF token (they have no browser
// session to protect). When auth is unconfigured
// (auth_token: ""), /mcp is open — same as the rest of
// the API.
//
// DNS-rebinding protection: the mark3labs/mcp-go server
// has a localhost-rebinding guard that rejects Host:
// headers other than localhost/127.0.0.1. We disable it
// here because ragabast is typically deployed behind a
// reverse proxy, and the auth layer is the actual
// security boundary — not the Host header. Operators
// who want DNS-rebinding protection can put the server
// behind a proxy that strips the Host header.
func mcpHTTPHandler(svc serviceAPI) http.Handler {
	mcpServer := mcp.NewServer(serviceAPIToMCPSvc{svc})
	return server.NewStreamableHTTPServer(
		mcpServer.MCPServer,
		server.WithEndpointPath("/mcp"),
		server.WithStateLess(true),
		server.WithDisableLocalhostProtection(true),
	)
}

// serviceAPIToMCPSvc adapts the web layer's wider
// serviceAPI to the narrower interface the MCP tools
// need. Lives in this file (not in internal/mcp) so the
// web layer owns the shape it exposes.
type serviceAPIToMCPSvc struct {
	svc serviceAPI
}

func (a serviceAPIToMCPSvc) HybridSearch(ctx context.Context, query string, limit int, filters service.SearchFilters, mode service.SearchMode) ([]models.SearchResult, error) {
	return a.svc.HybridSearch(ctx, query, limit, filters, mode)
}

func (a serviceAPIToMCPSvc) QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error) {
	return a.svc.QueryDebugWithOptions(ctx, query, limit, opts)
}

func (a serviceAPIToMCPSvc) ListDocumentsPaged(ctx context.Context, limit, offset int) ([]models.DocumentInfo, int, error) {
	return a.svc.ListDocumentsPaged(ctx, limit, offset)
}

func (a serviceAPIToMCPSvc) GetDocument(ctx context.Context, documentID string) (*models.Document, error) {
	return a.svc.GetDocument(ctx, documentID)
}
