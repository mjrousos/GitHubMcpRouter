// Package server wires up the MCP server and its transports.
package server

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/pkg/tools"
)

// Config holds the options for constructing and running the MCP server.
type Config struct {
	// Version is reported to clients during initialization.
	Version string
}

// New builds an MCP server with all tools registered. It does not start any
// transport; callers are responsible for running the returned server.
func New(cfg Config) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mcp-router",
		Version: cfg.Version,
	}, nil)

	tools.AddEcho(server)

	return server
}

// RunStdio builds the server and serves it over stdio until the context is
// cancelled, the client disconnects, or the process receives an interrupt.
func RunStdio(cfg Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := New(cfg)

	fmt.Fprintln(os.Stderr, "MCP Router running on stdio")

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("error running server: %w", err)
	}
	return nil
}
