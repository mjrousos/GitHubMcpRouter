package githubapp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v89/github"
)

const defaultAPIBaseURL = "https://api.github.com"

// Authenticator authenticates as a GitHub App and mints installation access
// tokens. Installations are resolved dynamically (by repository, organization,
// or user). A per-installation transport is cached and transparently refreshes
// the installation token, which expires after one hour.
//
// An Authenticator is safe for concurrent use.
type Authenticator struct {
	appID         int64
	apiBaseURL    string // no trailing slash
	appsTransport *ghinstallation.AppsTransport
	appClient     *github.Client // authenticated as the app (JWT); used to resolve installations

	mu            sync.Mutex
	installations map[int64]*ghinstallation.Transport
}

// New creates an Authenticator from the given configuration. The private key is
// parsed and validated up front, so an invalid key produces an error here.
func New(cfg Config) (*Authenticator, error) {
	apiBaseURL := defaultAPIBaseURL
	if v := strings.TrimSpace(cfg.APIBaseURL); v != "" {
		apiBaseURL = strings.TrimRight(v, "/")
	}

	appsTransport, err := ghinstallation.NewAppsTransport(http.DefaultTransport, cfg.AppID, cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("configuring GitHub App authentication: %w", err)
	}
	appsTransport.BaseURL = apiBaseURL

	a := &Authenticator{
		appID:         cfg.AppID,
		apiBaseURL:    apiBaseURL,
		appsTransport: appsTransport,
		installations: make(map[int64]*ghinstallation.Transport),
	}

	appClient, err := a.newGitHubClient(&http.Client{Transport: appsTransport})
	if err != nil {
		return nil, err
	}
	a.appClient = appClient

	return a, nil
}

// AppID returns the configured GitHub App ID.
func (a *Authenticator) AppID() int64 { return a.appID }

// Installation summarizes a single installation of the GitHub App.
type Installation struct {
	ID                  int64  `json:"id"`
	Account             string `json:"account"`              // login of the org or user
	AccountType         string `json:"account_type"`         // "Organization" or "User"
	RepositorySelection string `json:"repository_selection"` // "all" or "selected"
}

// Installations lists every installation of the app. It authenticates as the
// app itself (JWT) — this endpoint does not accept installation tokens — and
// pages through all results. Only the app ID and private key are required.
func (a *Authenticator) Installations(ctx context.Context) ([]Installation, error) {
	opts := &github.ListOptions{PerPage: 100}
	out := make([]Installation, 0)
	for {
		installations, resp, err := a.appClient.Apps.ListInstallations(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("listing app installations: %w", err)
		}
		for _, inst := range installations {
			out = append(out, Installation{
				ID:                  inst.GetID(),
				Account:             inst.GetAccount().GetLogin(),
				AccountType:         inst.GetTargetType(),
				RepositorySelection: inst.GetRepositorySelection(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// RepositoryToken returns an installation access token for the installation on
// the given repository.
func (a *Authenticator) RepositoryToken(ctx context.Context, owner, repo string) (string, error) {
	id, err := a.resolveRepository(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	return a.tokenForInstallation(ctx, id)
}

// OrganizationToken returns an installation access token for the installation
// on the given organization.
func (a *Authenticator) OrganizationToken(ctx context.Context, org string) (string, error) {
	id, err := a.resolveOrganization(ctx, org)
	if err != nil {
		return "", err
	}
	return a.tokenForInstallation(ctx, id)
}

// UserToken returns an installation access token for the installation on the
// given user account.
func (a *Authenticator) UserToken(ctx context.Context, user string) (string, error) {
	id, err := a.resolveUser(ctx, user)
	if err != nil {
		return "", err
	}
	return a.tokenForInstallation(ctx, id)
}

// RepositoryClient returns a go-github client authenticated as the installation
// on the given repository.
func (a *Authenticator) RepositoryClient(ctx context.Context, owner, repo string) (*github.Client, error) {
	id, err := a.resolveRepository(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	return a.installationClient(id)
}

// OrganizationClient returns a go-github client authenticated as the
// installation on the given organization.
func (a *Authenticator) OrganizationClient(ctx context.Context, org string) (*github.Client, error) {
	id, err := a.resolveOrganization(ctx, org)
	if err != nil {
		return nil, err
	}
	return a.installationClient(id)
}

// UserClient returns a go-github client authenticated as the installation on
// the given user account.
func (a *Authenticator) UserClient(ctx context.Context, user string) (*github.Client, error) {
	id, err := a.resolveUser(ctx, user)
	if err != nil {
		return nil, err
	}
	return a.installationClient(id)
}

func (a *Authenticator) resolveRepository(ctx context.Context, owner, repo string) (int64, error) {
	inst, _, err := a.appClient.Apps.GetRepositoryInstallation(ctx, owner, repo)
	if err != nil {
		return 0, fmt.Errorf("resolving installation for repository %s/%s: %w", owner, repo, err)
	}
	return installationID(inst)
}

func (a *Authenticator) resolveOrganization(ctx context.Context, org string) (int64, error) {
	inst, _, err := a.appClient.Apps.GetOrganizationInstallation(ctx, org)
	if err != nil {
		return 0, fmt.Errorf("resolving installation for organization %s: %w", org, err)
	}
	return installationID(inst)
}

func (a *Authenticator) resolveUser(ctx context.Context, user string) (int64, error) {
	inst, _, err := a.appClient.Apps.GetUserInstallation(ctx, user)
	if err != nil {
		return 0, fmt.Errorf("resolving installation for user %s: %w", user, err)
	}
	return installationID(inst)
}

func installationID(inst *github.Installation) (int64, error) {
	if inst == nil || inst.GetID() == 0 {
		return 0, fmt.Errorf("GitHub returned an installation without an ID")
	}
	return inst.GetID(), nil
}

// tokenForInstallation returns a valid installation access token, minting and
// caching it as needed.
func (a *Authenticator) tokenForInstallation(ctx context.Context, id int64) (string, error) {
	token, err := a.installationTransport(id).Token(ctx)
	if err != nil {
		return "", fmt.Errorf("minting installation token for installation %d: %w", id, err)
	}
	return token, nil
}

// installationClient returns a go-github client whose requests are
// authenticated with the installation's access token.
func (a *Authenticator) installationClient(id int64) (*github.Client, error) {
	return a.newGitHubClient(&http.Client{Transport: a.installationTransport(id)})
}

// installationTransport returns the cached ghinstallation transport for the
// installation, creating one on first use. The transport caches and refreshes
// the installation token internally.
func (a *Authenticator) installationTransport(id int64) *ghinstallation.Transport {
	a.mu.Lock()
	defer a.mu.Unlock()

	if t, ok := a.installations[id]; ok {
		return t
	}
	t := ghinstallation.NewFromAppsTransport(a.appsTransport, id)
	t.BaseURL = a.apiBaseURL
	a.installations[id] = t
	return t
}

// newGitHubClient builds a go-github client that sends requests through the
// given HTTP client, targeting the configured API base URL.
func (a *Authenticator) newGitHubClient(httpClient *http.Client) (*github.Client, error) {
	if a.apiBaseURL == defaultAPIBaseURL {
		return github.NewClient(github.WithHTTPClient(httpClient))
	}
	// go-github requires the base URL to end with a slash. WithURLs sets it
	// verbatim (unlike WithEnterpriseURLs, which appends "/api/v3/").
	base := a.apiBaseURL + "/"
	return github.NewClient(github.WithHTTPClient(httpClient), github.WithURLs(&base, &base))
}
