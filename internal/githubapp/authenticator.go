package githubapp

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	jwt "github.com/golang-jwt/jwt/v4"
	"github.com/google/go-github/v89/github"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	defaultTimeout    = 30 * time.Second
)

// Authenticator authenticates as a GitHub App and mints installation access
// tokens. Installations are resolved dynamically (by repository, organization,
// or user). A per-installation transport is cached and transparently refreshes
// the installation token, which expires after one hour.
//
// An Authenticator is safe for concurrent use.
type Authenticator struct {
	appID         int64
	apiBaseURL    string // no trailing slash
	privateKey    *rsa.PrivateKey
	baseTransport http.RoundTripper
	timeout       time.Duration

	appsTransport *ghinstallation.AppsTransport // authenticated as the app (JWT); resolution only
	appClient     *github.Client

	mu            sync.Mutex
	installations map[int64]*ghinstallation.Transport
}

// New creates an Authenticator from the given configuration. The private key is
// parsed and validated up front, so an invalid key produces an error here.
func New(cfg Config) (*Authenticator, error) {
	if cfg.AppID <= 0 {
		return nil, fmt.Errorf("GitHub App ID must be positive, got %d", cfg.AppID)
	}

	apiBaseURL := defaultAPIBaseURL
	if v := strings.TrimSpace(cfg.APIBaseURL); v != "" {
		apiBaseURL = strings.TrimRight(v, "/")
	}

	key, err := jwt.ParseRSAPrivateKeyFromPEM(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("configuring GitHub App authentication: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	a := &Authenticator{
		appID:         cfg.AppID,
		apiBaseURL:    apiBaseURL,
		privateKey:    key,
		baseTransport: http.DefaultTransport,
		timeout:       timeout,
		installations: make(map[int64]*ghinstallation.Transport),
	}

	// A dedicated AppsTransport for resolving installations (app JWT). Each
	// installation gets its own AppsTransport (see installationTransport) so
	// that ghinstallation's token refresh — which mutates the AppsTransport —
	// never races across installations.
	a.appsTransport = a.newAppsTransport()
	appClient, err := a.newGitHubClient(a.appsTransport)
	if err != nil {
		return nil, err
	}
	a.appClient = appClient

	return a, nil
}

// AppID returns the configured GitHub App ID.
func (a *Authenticator) AppID() int64 { return a.appID }

// newAppsTransport builds an app-JWT transport over the shared base transport.
func (a *Authenticator) newAppsTransport() *ghinstallation.AppsTransport {
	atr := ghinstallation.NewAppsTransportFromPrivateKey(a.baseTransport, a.appID, a.privateKey)
	atr.BaseURL = a.apiBaseURL
	return atr
}

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
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	token, err := a.installationTransport(id).Token(ctx)
	if err != nil {
		// On a non-2xx response ghinstallation returns an *HTTPError whose
		// response body is left open; close it to avoid leaking connections.
		drainAndCloseHTTPError(err)
		return "", fmt.Errorf("minting installation token for installation %d: %w", id, err)
	}
	return token, nil
}

// installationClient returns a go-github client whose requests are
// authenticated with the installation's access token.
func (a *Authenticator) installationClient(id int64) (*github.Client, error) {
	return a.newGitHubClient(&errorBodyClosingTransport{base: a.installationTransport(id)})
}

// installationTransport returns the cached ghinstallation transport for the
// installation, creating one on first use. Each transport wraps its own
// AppsTransport (sharing only the base http.RoundTripper) so that token refresh,
// which mutates the AppsTransport, is isolated per installation.
func (a *Authenticator) installationTransport(id int64) *ghinstallation.Transport {
	a.mu.Lock()
	defer a.mu.Unlock()

	if t, ok := a.installations[id]; ok {
		return t
	}
	t := ghinstallation.NewFromAppsTransport(a.newAppsTransport(), id)
	t.BaseURL = a.apiBaseURL
	a.installations[id] = t
	return t
}

// newGitHubClient builds a go-github client that sends requests through the
// given round tripper, targeting the configured API base URL, with a bounded
// timeout and a redirect policy that refuses to leak credentials.
func (a *Authenticator) newGitHubClient(rt http.RoundTripper) (*github.Client, error) {
	httpClient := &http.Client{
		Transport:     rt,
		Timeout:       a.timeout,
		CheckRedirect: checkRedirect,
	}
	if a.apiBaseURL == defaultAPIBaseURL {
		return github.NewClient(github.WithHTTPClient(httpClient))
	}
	// go-github requires the base URL to end with a slash. WithURLs sets it
	// verbatim (unlike WithEnterpriseURLs, which appends "/api/v3/").
	base := a.apiBaseURL + "/"
	return github.NewClient(github.WithHTTPClient(httpClient), github.WithURLs(&base, &base))
}

// checkRedirect refuses redirects that would send credentials to a different
// host or downgrade from HTTPS, since the authenticating transport re-injects
// the token on every hop.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	origin := via[0].URL
	if req.URL.Host != origin.Host {
		return fmt.Errorf("refusing cross-host redirect from %q to %q", origin.Host, req.URL.Host)
	}
	if origin.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing to downgrade from https to %q on redirect", req.URL.Scheme)
	}
	return nil
}

// errorBodyClosingTransport wraps a RoundTripper and, on error, closes any
// *ghinstallation.HTTPError response body that ghinstallation leaves open.
type errorBodyClosingTransport struct {
	base http.RoundTripper
}

func (t *errorBodyClosingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		drainAndCloseHTTPError(err)
	}
	return resp, err
}

// drainAndCloseHTTPError closes the response body carried by a
// *ghinstallation.HTTPError (which ghinstallation intentionally leaves open on
// non-2xx token responses), if the error chain contains one.
func drainAndCloseHTTPError(err error) {
	var httpErr *ghinstallation.HTTPError
	if errors.As(err, &httpErr) && httpErr.Response != nil && httpErr.Response.Body != nil {
		_, _ = io.Copy(io.Discard, httpErr.Response.Body)
		_ = httpErr.Response.Body.Close()
	}
}
