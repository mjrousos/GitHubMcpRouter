package downstream

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/mjrousos/GitHubMcpRouter/internal/githubapp"
)

// InstallationLister lists the GitHub App's installations. It is satisfied by
// *githubapp.Authenticator.
type InstallationLister interface {
	Installations(ctx context.Context) ([]githubapp.Installation, error)
}

// directory resolves an owner (organization or user login) to its GitHub App
// installation, restricted to an optional allow-list. It caches the installation
// list and refreshes once on a miss, so a newly added installation is picked up
// without a restart.
type directory struct {
	lister  InstallationLister
	allowed map[string]struct{} // lowercased logins; nil => all allowed

	mu      sync.Mutex
	byLogin map[string]githubapp.Installation // lowercased login -> installation
	loaded  bool
}

func newDirectory(lister InstallationLister, allowed map[string]struct{}) *directory {
	return &directory{lister: lister, allowed: allowed}
}

func (d *directory) isAllowed(login string) bool {
	if d.allowed == nil {
		return true
	}
	_, ok := d.allowed[strings.ToLower(login)]
	return ok
}

// load (re)builds the login->installation map, applying the allow-list. Callers
// must hold d.mu.
func (d *directory) load(ctx context.Context) error {
	installations, err := d.lister.Installations(ctx)
	if err != nil {
		return fmt.Errorf("listing installations: %w", err)
	}
	byLogin := make(map[string]githubapp.Installation, len(installations))
	for _, inst := range installations {
		if inst.Account == "" || !d.isAllowed(inst.Account) {
			continue
		}
		byLogin[strings.ToLower(inst.Account)] = inst
	}
	d.byLogin = byLogin
	d.loaded = true
	return nil
}

// lookup returns the installation for the given owner, refreshing once on a miss.
func (d *directory) lookup(ctx context.Context, owner string) (githubapp.Installation, error) {
	key := strings.ToLower(strings.TrimSpace(owner))
	if key == "" {
		return githubapp.Installation{}, fmt.Errorf("owner is required")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.loaded {
		if err := d.load(ctx); err != nil {
			return githubapp.Installation{}, err
		}
	}
	if inst, ok := d.byLogin[key]; ok {
		return inst, nil
	}
	// Refresh once in case the installation was added since we last listed.
	if err := d.load(ctx); err != nil {
		return githubapp.Installation{}, err
	}
	if inst, ok := d.byLogin[key]; ok {
		return inst, nil
	}
	if d.allowed != nil {
		if _, ok := d.allowed[key]; !ok {
			return githubapp.Installation{}, fmt.Errorf("owner %q is not permitted by %s", owner, EnvAllowedOrgs)
		}
	}
	return githubapp.Installation{}, fmt.Errorf("no GitHub App installation found for owner %q", owner)
}

// all returns every installation permitted by the allow-list.
func (d *directory) all(ctx context.Context) ([]githubapp.Installation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.loaded {
		if err := d.load(ctx); err != nil {
			return nil, err
		}
	}
	out := make([]githubapp.Installation, 0, len(d.byLogin))
	for _, inst := range d.byLogin {
		out = append(out, inst)
	}
	return out, nil
}
