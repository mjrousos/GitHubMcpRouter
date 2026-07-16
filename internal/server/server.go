// Package server wires up the MCP server and its transports.
package server

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/githubapp"
	"github.com/mjrousos/GitHubMcpRouter/pkg/tools"
)

// Config holds the options for constructing and running the MCP server.
type Config struct {
	// Version is reported to clients during initialization.
	Version string

	// Authenticator authenticates with GitHub as a GitHub App. It is optional:
	// when nil, the server runs without GitHub access. RunStdio populates it
	// from the environment when it is not supplied.
	Authenticator *githubapp.Authenticator
}

// New builds an MCP server with all tools registered. It does not start any
// transport; callers are responsible for running the returned server.
func New(cfg Config) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mcp-router",
		Version: cfg.Version,
	}, nil)

	tools.AddEcho(server)

	// Register GitHub-backed tools only when the app is configured to
	// authenticate; without credentials they would fail on every call.
	if cfg.Authenticator != nil {
		tools.AddListInstallations(server, cfg.Authenticator)
	}

	return server
}

// RunStdio builds the server and serves it over stdio until the context is
// cancelled, the client disconnects, or the process receives an interrupt.
func RunStdio(cfg Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.Authenticator == nil {
		auth, err := loadAuthenticatorFromEnv()
		if err != nil {
			return err
		}
		cfg.Authenticator = auth
	}

	server := New(cfg)

	fmt.Fprintln(os.Stderr, "MCP Router running on stdio")

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("error running server: %w", err)
	}
	return nil
}

// loadAuthenticatorFromEnv builds a GitHub App authenticator from environment
// variables. It returns a nil authenticator (and logs) when GitHub App
// credentials are not configured, so the server can still run.
func loadAuthenticatorFromEnv() (*githubapp.Authenticator, error) {
	ghCfg, err := githubapp.LoadConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("loading GitHub App configuration: %w", err)
	}
	if ghCfg == nil {
		fmt.Fprintln(os.Stderr, "GitHub App authentication not configured; GitHub tools will be unavailable")
		return nil, nil
	}

	auth, err := githubapp.New(*ghCfg)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "GitHub App authentication enabled (app ID %d)\n", auth.AppID())
	return auth, nil
}
