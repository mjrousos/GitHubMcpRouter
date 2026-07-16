package downstream

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/githubapp"
)

// OwnerClient pairs an owner (organization or user login) with the caller for
// its downstream github-mcp-server.
type OwnerClient struct {
	Owner  string
	Caller ToolCaller
}

// Router delegates MCP tool calls to per-installation github-mcp-server children.
// It resolves an owner to an installation and forwards the call, or fans out to
// every allowed installation. A Router is safe for concurrent use.
type Router struct {
	dir     *directory
	manager *manager
	cancel  context.CancelFunc // cancels the children's lifetime context (nil in tests)
}

// Config configures a Router that spawns real github-mcp-server children.
type Config struct {
	// Lister enumerates the GitHub App's installations.
	Lister InstallationLister
	// AllowedOrgs is the raw GITHUB_APP_ALLOWED_ORGS value (comma-separated).
	AllowedOrgs string
	// BinaryPath is the resolved path to the github-mcp-server executable.
	BinaryPath string
	// BaseEnv is the environment children inherit (typically os.Environ()).
	BaseEnv []string
	// Version is reported as the client implementation version to children.
	Version string
}

// New builds a Router that spawns github-mcp-server child processes on demand.
func New(cfg Config) *Router {
	ctx, cancel := context.WithCancel(context.Background())
	sp := &spawner{
		ctx:        ctx,
		binaryPath: cfg.BinaryPath,
		baseEnv:    cfg.BaseEnv,
		clientInfo: &mcp.Implementation{Name: "mcp-router", Version: cfg.Version},
	}
	return &Router{
		dir:     newDirectory(cfg.Lister, parseAllowedOrgs(cfg.AllowedOrgs)),
		manager: newManager(sp.connect),
		cancel:  cancel,
	}
}

// ClientForOwner returns the downstream caller for the given owner, starting the
// child process if necessary.
func (r *Router) ClientForOwner(ctx context.Context, owner string) (ToolCaller, error) {
	inst, err := r.dir.lookup(ctx, owner)
	if err != nil {
		return nil, err
	}
	return r.manager.caller(ctx, inst.ID)
}

// AllClients returns a caller for every allowed installation. Children are
// started concurrently. It is best-effort: installations whose child fails to
// start are omitted and described in the returned error, which is non-nil only
// when at least one failed to start.
func (r *Router) AllClients(ctx context.Context) ([]OwnerClient, error) {
	installations, err := r.dir.all(ctx)
	if err != nil {
		return nil, err
	}

	type result struct {
		owner  string
		caller ToolCaller
		err    error
	}
	results := make([]result, len(installations))
	var wg sync.WaitGroup
	for i, inst := range installations {
		wg.Add(1)
		go func(i int, inst githubapp.Installation) {
			defer wg.Done()
			caller, err := r.manager.caller(ctx, inst.ID)
			results[i] = result{owner: inst.Account, caller: caller, err: err}
		}(i, inst)
	}
	wg.Wait()

	clients := make([]OwnerClient, 0, len(results))
	var errs []error
	for _, res := range results {
		if res.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", res.owner, res.err))
			continue
		}
		clients = append(clients, OwnerClient{Owner: res.owner, Caller: res.caller})
	}
	return clients, errors.Join(errs...)
}

// Close shuts down all child processes.
func (r *Router) Close() error {
	err := r.manager.Close()
	if r.cancel != nil {
		r.cancel()
	}
	return err
}
