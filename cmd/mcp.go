package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"
	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/mcp"
	"github.com/ragabast/internal/service"
)

// MCPCmd runs an MCP server. The primary use case is
// remote MCP clients reaching a central ragabast install
// — that's served by the existing `ragabast serve` web
// server with `server.mcp_http_enabled: true`.
//
// `ragabast mcp serve --stdio` is the secondary, local-
// operator workflow: the operator runs the binary
// themselves and pipes stdin/stdout to a local MCP
// client (Claude Desktop, opencode, etc.). No auth —
// the operator IS the client.
//
// `--transport http` runs the streamable-HTTP server on
// a separate port (default 8081). This is documented but
// rarely useful — operators who want HTTP MCP should
// prefer `ragabast serve` with `server.mcp_http_enabled`
// so auth, CSRF, and rate-limit all apply uniformly.
type MCPCmd struct {
	Serve MCPServeCmd `cmd:"" help:"Run the MCP server (stdio by default)"`
}

type MCPServeCmd struct {
	Stdio    bool   `name:"stdio" help:"Run over stdin/stdout (default transport)."`
	HTTP     bool   `name:"http" help:"Run over streamable HTTP on the given address."`
	HTTPAddr string `name:"http-addr" default:":8081" help:"Listen address for --http."`
}

// Run starts the MCP server and blocks until ctx is
// cancelled or the transport returns. Reads the same
// config + service that `ragabast serve` uses.
func (c *MCPServeCmd) Run() error {
	parentCtx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return withService(ConfigOpts{}, func(ctx context.Context, cfg *config.Config, svc *service.Service) error {
		mcpServer := mcp.NewServer(svc)
		_ = parentCtx // shadowed by closure ctx; keep parent alive for goroutines if any

		switch {
		case c.HTTP:
			httpServer := server.NewStreamableHTTPServer(
				mcpServer.MCPServer,
				server.WithEndpointPath("/mcp"),
				server.WithDisableLocalhostProtection(true),
			)
			fmt.Fprintf(os.Stderr, "ragabast MCP HTTP server listening on %s (path /mcp)\n", c.HTTPAddr)
			if err := httpServer.Start(c.HTTPAddr); err != nil {
				return fmt.Errorf("MCP HTTP server: %w", err)
			}
			<-ctx.Done()
			shutdownCtx, cancelShutdown := context.WithCancel(ctx)
			defer cancelShutdown()
			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "MCP HTTP shutdown error: %v\n", err)
			}
			return nil

		default: // stdio (default)
			stdioServer := server.NewStdioServer(mcpServer.MCPServer)
			fmt.Fprintf(os.Stderr, "ragabast MCP stdio server ready\n")
			if err := stdioServer.Listen(ctx, os.Stdin, os.Stdout); err != nil {
				return fmt.Errorf("MCP stdio server: %w", err)
			}
			return nil
		}
	})
}
