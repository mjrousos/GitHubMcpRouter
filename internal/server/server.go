// Package server wires up the MCP server and its transports.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
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

	// SessionTimeout closes idle MCP sessions after this duration, bounding
	// resource use from clients that disconnect without cleaning up. When zero,
	// DefaultSessionTimeout is used; a negative value disables the timeout.
	SessionTimeout time.Duration

	// MaxRequestBytes caps the size of a request body accepted at the MCP
	// endpoint. When zero, DefaultMaxRequestBytes is used.
	MaxRequestBytes int64
}

// DefaultHTTPAddress is the address the HTTP transport listens on when none is
// configured. It binds to localhost so the server is not exposed on the network
// by default.
const DefaultHTTPAddress = "localhost:8080"

// MCPPath is the HTTP path that serves the MCP endpoint.
const MCPPath = "/mcp"

// Defaults for the HTTP transport.
const (
	// httpShutdownTimeout bounds how long we wait for in-flight requests to
	// finish during a graceful shutdown.
	httpShutdownTimeout = 10 * time.Second

	// DefaultSessionTimeout closes MCP sessions that receive no requests for
	// this long, so abandoned sessions don't accumulate.
	DefaultSessionTimeout = 30 * time.Minute

	// DefaultMaxRequestBytes bounds request bodies at the MCP endpoint.
	DefaultMaxRequestBytes = 4 << 20 // 4 MiB

	// httpReadTimeout bounds how long a client may take to send a request
	// (headers + body). It does not limit the response, so long-lived SSE
	// streams are unaffected.
	httpReadTimeout = 30 * time.Second

	// httpIdleTimeout bounds how long an idle keep-alive connection is kept.
	httpIdleTimeout = 120 * time.Second
)

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

	httpServer := newHTTPServer(ctx, cfg, httpCfg)

	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", httpServer.Addr, err)
	}

	fmt.Fprintf(os.Stderr, "MCP Router listening on http://%s%s\n", httpServer.Addr, MCPPath)

	return serveHTTP(ctx, httpServer, listener)
}

// newHTTPServer builds the *http.Server that serves the MCP endpoint and health
// check. Request contexts derive from ctx (via BaseContext) so that cancelling
// ctx unblocks long-lived SSE handlers and lets a graceful shutdown complete
// promptly. The returned server is not yet listening.
func newHTTPServer(ctx context.Context, cfg Config, httpCfg HTTPConfig) *http.Server {
	address := httpCfg.Address
	if address == "" {
		address = DefaultHTTPAddress
	}
	sessionTimeout := httpCfg.SessionTimeout
	if sessionTimeout == 0 {
		sessionTimeout = DefaultSessionTimeout
	}
	if sessionTimeout < 0 {
		sessionTimeout = 0 // negative disables the SDK's idle-session timeout
	}
	maxBytes := httpCfg.MaxRequestBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxRequestBytes
	}

	// A single server backs every session; the SDK permits returning the same
	// server for each request.
	server := New(cfg)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout: sessionTimeout,
	})
	// Bound request bodies, then apply cross-origin protection (the MCP spec
	// requires validating the Origin of incoming connections).
	endpoint := http.NewCrossOriginProtection().Handler(maxBytesHandler(mcpHandler, maxBytes))

	mux := http.NewServeMux()
	mux.Handle(MCPPath, endpoint)
	mux.Handle(MCPPath+"/", endpoint)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       httpReadTimeout,
		IdleTimeout:       httpIdleTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}

// serveHTTP serves srv on ln until ctx is cancelled, then gracefully shuts it
// down. It returns the first error from serving or shutting down.
//
// It waits on either the serve result or context cancellation, and only blocks
// for a graceful shutdown when it initiates one. This avoids leaking a goroutine
// (or blocking forever) if srv.Serve returns for a reason other than this
// function's own shutdown — e.g. srv.Close() called elsewhere or the listener
// closing.
func serveHTTP(ctx context.Context, srv *http.Server, ln net.Listener) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		// The server stopped on its own, before we asked it to.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("error running HTTP server: %w", err)
		}
		return nil
	case <-ctx.Done():
		// We initiate a graceful shutdown, then wait for Serve to return (it
		// returns http.ErrServerClosed once Shutdown completes).
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		shutdownErr := srv.Shutdown(shutdownCtx)
		<-serveErr
		if shutdownErr != nil {
			return fmt.Errorf("error shutting down HTTP server: %w", shutdownErr)
		}
		return nil
	}
}

// maxBytesHandler limits the request body to limit bytes before delegating to
// next. A GET (e.g. the standalone SSE stream) carries no body, so this is a
// no-op for those requests.
func maxBytesHandler(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// prepareFromEnv fills in the Authenticator and Router from the environment when
// they were not supplied, and returns a cleanup function that releases any
// resources (e.g. downstream child processes).
func (cfg *Config) prepareFromEnv() (func(), error) {
	cleanup := func() {}

	// ghCfg is the GitHub App configuration loaded from the environment (nil
	// when the Authenticator was supplied externally or auth is unconfigured).
	// It carries the resolved app ID and private key used to give spawned
	// children canonical credentials when the operator used the aliases.
	var ghCfg *githubapp.Config
	if cfg.Authenticator == nil {
		auth, loaded, err := loadAuthenticatorFromEnv()
		if err != nil {
			return cleanup, err
		}
		cfg.Authenticator = auth
		ghCfg = loaded
	}

	if cfg.Router == nil && cfg.Authenticator != nil {
		if router := buildRouterFromEnv(cfg.Authenticator, ghCfg, cfg.Version); router != nil {
			cfg.Router = router
			cleanup = func() { _ = router.Close() }
		}
	}

	return cleanup, nil
}

// buildRouterFromEnv constructs the organization router from environment
// variables. It returns nil (and logs) when the github-mcp-server binary cannot
// be found, so the server still runs with its other tools. ghCfg, when non-nil,
// is used to give children canonical GitHub App credentials.
func buildRouterFromEnv(auth *githubapp.Authenticator, ghCfg *githubapp.Config, version string) *downstream.Router {
	binary, err := downstream.ResolveBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "GitHub organization routing disabled: %v\n", err)
		return nil
	}
	router := downstream.New(downstream.Config{
		Lister:      auth,
		AllowedOrgs: os.Getenv(downstream.EnvAllowedOrgs),
		BinaryPath:  binary,
		BaseEnv:     childBaseEnv(os.Environ(), ghCfg),
		Version:     version,
	})
	fmt.Fprintf(os.Stderr, "GitHub organization routing enabled (github-mcp-server: %s)\n", binary)
	return router
}

// childBaseEnv augments env with the canonical GitHub App variables that spawned
// github-mcp-server children expect (GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY),
// when the router itself was configured via the MCP_ROUTER_* aliases. The
// children only understand the GITHUB_ names, so without this they would fail to
// authenticate.
//
// Whether a canonical value is already available to the children is decided from
// which variable supplied it (ghCfg's sources) rather than by scanning env:
// those sources come from os.Getenv, so this stays correct under platform env
// semantics (e.g. Windows' case-insensitive names). A value the operator set
// under a canonical name — including a key path — is inherited by the children
// as-is and is never overwritten or duplicated inline.
func childBaseEnv(env []string, ghCfg *githubapp.Config) []string {
	if ghCfg == nil {
		return env
	}
	// A source of the alias implies the canonical name was unset (lookupEnv
	// prefers the canonical name), so the children need it supplied.
	if ghCfg.AppIDSource == githubapp.EnvAppIDAlias {
		env = append(env, fmt.Sprintf("%s=%d", githubapp.EnvAppID, ghCfg.AppID))
		fmt.Fprintf(os.Stderr, "Propagating %s to github-mcp-server children (resolved from %s)\n",
			githubapp.EnvAppID, ghCfg.AppIDSource)
	}
	// Only the inline alias needs bridging: a canonical inline key or a key path
	// is already visible to the children under a name they understand.
	if ghCfg.PrivateKeySource == githubapp.EnvPrivateKeyAlias {
		env = append(env, githubapp.EnvPrivateKey+"="+string(ghCfg.PrivateKey))
		fmt.Fprintf(os.Stderr, "Propagating %s to github-mcp-server children (resolved from %s)\n",
			githubapp.EnvPrivateKey, ghCfg.PrivateKeySource)
	}
	return env
}

// loadAuthenticatorFromEnv builds a GitHub App authenticator from environment
// variables. It returns a nil authenticator (and logs) when GitHub App
// credentials are not configured, so the server can still run. It also returns
// the loaded configuration (nil when unconfigured) for downstream propagation.
func loadAuthenticatorFromEnv() (*githubapp.Authenticator, *githubapp.Config, error) {
	ghCfg, err := githubapp.LoadConfigFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("loading GitHub App configuration: %w", err)
	}
	if ghCfg == nil {
		fmt.Fprintln(os.Stderr, "GitHub App authentication not configured; GitHub tools will be unavailable")
		return nil, nil, nil
	}

	auth, err := githubapp.New(*ghCfg)
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(os.Stderr, "GitHub App authentication enabled (app ID %d from %s, private key from %s)\n",
		auth.AppID(), ghCfg.AppIDSource, ghCfg.PrivateKeySource)
	return auth, ghCfg, nil
}
