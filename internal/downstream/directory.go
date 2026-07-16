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
//
// The (potentially slow, paged) GitHub API call happens under loadMu, never
// under mu, so cache reads are not blocked by network I/O. loadMu also dedupes
// concurrent refreshes. The cached map is replaced wholesale (copy-on-write), so
// a snapshot can be read without holding a lock.
type directory struct {
	lister  InstallationLister
	allowed map[string]struct{} // lowercased logins; nil => all allowed

	mu      sync.Mutex
	byLogin map[string]githubapp.Installation // lowercased login -> installation
	loaded  bool

	loadMu sync.Mutex // serializes refreshes; held during network I/O, mu is not
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

// snapshot returns the current login->installation map and whether it has been
// loaded. The returned map is never mutated in place, so it is safe to read
// without holding a lock.
func (d *directory) snapshot() (map[string]githubapp.Installation, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.byLogin, d.loaded
}

// refresh lists installations and replaces the cached map. The network call is
// made without holding mu; loadMu (held by the caller) dedupes concurrent
// refreshes.
func (d *directory) refresh(ctx context.Context) error {
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

	d.mu.Lock()
	d.byLogin = byLogin
	d.loaded = true
	d.mu.Unlock()
	return nil
}

// lookup returns the installation for the given owner, refreshing once on a miss.
func (d *directory) lookup(ctx context.Context, owner string) (githubapp.Installation, error) {
	key := strings.ToLower(strings.TrimSpace(owner))
	if key == "" {
		return githubapp.Installation{}, fmt.Errorf("owner is required")
	}

	// Fast path: a cache hit never blocks on network I/O.
	if byLogin, loaded := d.snapshot(); loaded {
		if inst, ok := byLogin[key]; ok {
			return inst, nil
		}
	}

	// Miss (or not yet loaded): refresh under loadMu, without holding mu during
	// the network call. Re-check first in case another goroutine just refreshed.
	d.loadMu.Lock()
	defer d.loadMu.Unlock()

	if byLogin, loaded := d.snapshot(); loaded {
		if inst, ok := byLogin[key]; ok {
			return inst, nil
		}
	}
	if err := d.refresh(ctx); err != nil {
		return githubapp.Installation{}, err
	}
	if byLogin, _ := d.snapshot(); true {
		if inst, ok := byLogin[key]; ok {
			return inst, nil
		}
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
	byLogin, loaded := d.snapshot()
	if !loaded {
		d.loadMu.Lock()
		byLogin, loaded = d.snapshot()
		if !loaded {
			if err := d.refresh(ctx); err != nil {
				d.loadMu.Unlock()
				return nil, err
			}
			byLogin, _ = d.snapshot()
		}
		d.loadMu.Unlock()
	}

	out := make([]githubapp.Installation, 0, len(byLogin))
	for _, inst := range byLogin {
		out = append(out, inst)
	}
	return out, nil
}
