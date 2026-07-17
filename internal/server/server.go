// Package server wires up the MCP server and its transports.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/downstream"
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

	// Router delegates GitHub tool calls to per-organization github-mcp-server
	// child processes. It is optional: when nil, the GitHub organization tools
	// are not registered. RunStdio populates it from the environment when it is
	// not supplied.
	Router *downstream.Router
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

	// Register the organization-routing tools only when a router is available
	// (GitHub App auth configured and the github-mcp-server binary found).
	if cfg.Router != nil {
		tools.AddGetFileContents(server, cfg.Router)
		tools.AddSearchCode(server, cfg.Router)
	}

	return server
}

// RunStdio builds the server and serves it over stdio until the context is
// cancelled, the client disconnects, or the process receives an interrupt.
func RunStdio(cfg Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cleanup, err := cfg.prepareFromEnv()
	if err != nil {
		return err
	}
	defer cleanup()

	server := New(cfg)

	fmt.Fprintln(os.Stderr, "MCP Router running on stdio")

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("error running server: %w", err)
	}
	return nil
}

// HTTPConfig configures the HTTP (streamable) transport.
type HTTPConfig struct {
	// Address is the TCP address to listen on, e.g. "localhost:8080". When
	// empty, DefaultHTTPAddress is used.
	Address string
}

// DefaultHTTPAddress is the address the HTTP transport listens on when none is
// configured. It binds to localhost so the server is not exposed on the network
// by default.
const DefaultHTTPAddress = "localhost:8080"

// MCPPath is the HTTP path that serves the MCP endpoint.
const MCPPath = "/mcp"

// httpShutdownTimeout bounds how long we wait for in-flight requests to finish
// during a graceful shutdown.
const httpShutdownTimeout = 10 * time.Second

// RunHTTP builds the server and serves it over the streamable HTTP transport
// until the context is cancelled or the process receives an interrupt. The MCP
// endpoint is served at MCPPath; a plain-text health check is served at
// "/healthz".
func RunHTTP(cfg Config, httpCfg HTTPConfig) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cleanup, err := cfg.prepareFromEnv()
	if err != nil {
		return err
	}
	defer cleanup()

	address := httpCfg.Address
	if address == "" {
		address = DefaultHTTPAddress
	}

	// A single server backs every session; the SDK permits returning the same
	// server for each request.
	server := New(cfg)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)

	mux := http.NewServeMux()
	mux.Handle(MCPPath, mcpHandler)
	mux.Handle(MCPPath+"/", mcpHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	httpServer := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Trigger a graceful shutdown when the context is cancelled (signal).
	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		shutdownErr <- httpServer.Shutdown(shutdownCtx)
	}()

	fmt.Fprintf(os.Stderr, "MCP Router listening on http://%s%s\n", address, MCPPath)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("error running HTTP server: %w", err)
	}
	if err := <-shutdownErr; err != nil {
		return fmt.Errorf("error shutting down HTTP server: %w", err)
	}
	return nil
}

// prepareFromEnv fills in the Authenticator and Router from the environment when
// they were not supplied, and returns a cleanup function that releases any
// resources (e.g. downstream child processes).
func (cfg *Config) prepareFromEnv() (func(), error) {
	cleanup := func() {}

	if cfg.Authenticator == nil {
		auth, err := loadAuthenticatorFromEnv()
		if err != nil {
			return cleanup, err
		}
		cfg.Authenticator = auth
	}

	if cfg.Router == nil && cfg.Authenticator != nil {
		if router := buildRouterFromEnv(cfg.Authenticator, cfg.Version); router != nil {
			cfg.Router = router
			cleanup = func() { _ = router.Close() }
		}
	}

	return cleanup, nil
}

// buildRouterFromEnv constructs the organization router from environment
// variables. It returns nil (and logs) when the github-mcp-server binary cannot
// be found, so the server still runs with its other tools.
func buildRouterFromEnv(auth *githubapp.Authenticator, version string) *downstream.Router {
	binary, err := downstream.ResolveBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "GitHub organization routing disabled: %v\n", err)
		return nil
	}
	router := downstream.New(downstream.Config{
		Lister:      auth,
		AllowedOrgs: os.Getenv(downstream.EnvAllowedOrgs),
		BinaryPath:  binary,
		BaseEnv:     os.Environ(),
		Version:     version,
	})
	fmt.Fprintf(os.Stderr, "GitHub organization routing enabled (github-mcp-server: %s)\n", binary)
	return router
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
