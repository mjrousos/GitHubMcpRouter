// Package githubapp authenticates with GitHub as a GitHub App.
//
// The server acts server-to-server: it signs a short-lived JWT as the app,
// resolves the relevant installation dynamically (by repository, organization,
// or user), and exchanges the JWT for a 1-hour installation access token that
// is used for subsequent API requests. Token minting and refresh are handled by
// ghinstallation; installation resolution uses go-github.
package githubapp

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables that configure GitHub App authentication.
//
// The app ID and inline private key each accept an MCP_ROUTER_-prefixed alias in
// addition to the GITHUB_-prefixed name, so the server can be configured in
// environments where the GITHUB_ names are reserved or otherwise unavailable.
// When both a primary and its alias are set, the primary (GITHUB_) name wins.
const (
	EnvAppID           = "GITHUB_APP_ID"
	EnvAppIDAlias      = "MCP_ROUTER_APP_ID"
	EnvPrivateKeyPath  = "GITHUB_APP_PRIVATE_KEY_PATH"
	EnvPrivateKey      = "GITHUB_APP_PRIVATE_KEY"
	EnvPrivateKeyAlias = "MCP_ROUTER_APP_PRIVATE_KEY"
	EnvAPIURL          = "GITHUB_API_URL"
)

// appIDEnvVars and privateKeyEnvVars list the accepted environment variable
// names for the app ID and the inline private key, in precedence order. The
// first that is set wins.
var (
	appIDEnvVars      = []string{EnvAppID, EnvAppIDAlias}
	privateKeyEnvVars = []string{EnvPrivateKey, EnvPrivateKeyAlias}
)

// Config holds the settings needed to authenticate as a GitHub App.
type Config struct {
	// AppID is the numeric GitHub App ID (used as the JWT issuer).
	AppID int64

	// PrivateKey is the PEM-encoded RSA private key used to sign app JWTs.
	PrivateKey []byte

	// APIBaseURL overrides the GitHub API base URL (for GitHub Enterprise
	// Server). When empty, the public GitHub API is used.
	APIBaseURL string

	// Timeout bounds each outbound GitHub API request (including token
	// refresh). When zero, a sensible default is used.
	Timeout time.Duration

	// AppIDSource and PrivateKeySource name the environment variables that
	// supplied the app ID and private key, respectively. They are populated by
	// LoadConfigFromEnv purely for diagnostics and are ignored when
	// constructing an Authenticator.
	AppIDSource      string
	PrivateKeySource string
}

// LoadConfigFromEnv reads GitHub App configuration from environment variables.
//
// It returns (nil, nil) when none of the GitHub App variables are set, so the
// server can run without GitHub access. It returns an error when configuration
// is partially present or invalid.
//
// The app ID is read from GITHUB_APP_ID or, as an alias, MCP_ROUTER_APP_ID. The
// private key is read from GITHUB_APP_PRIVATE_KEY_PATH (preferred) or, if that
// is unset, the inline GITHUB_APP_PRIVATE_KEY or its alias
// MCP_ROUTER_APP_PRIVATE_KEY. When both a primary and an alias are set, the
// primary wins. Config.AppIDSource and Config.PrivateKeySource record which
// variables were actually used.
func LoadConfigFromEnv() (*Config, error) {
	appIDRaw, appIDSource := lookupEnv(appIDEnvVars...)
	keyPath := strings.TrimSpace(os.Getenv(EnvPrivateKeyPath))
	keyInline, keyInlineSource := lookupEnv(privateKeyEnvVars...)
	apiURL := strings.TrimSpace(os.Getenv(EnvAPIURL))

	// Nothing configured at all: GitHub App auth is simply disabled.
	if appIDRaw == "" && keyPath == "" && keyInline == "" {
		return nil, nil
	}

	if appIDRaw == "" {
		return nil, fmt.Errorf("a GitHub App ID must be set (%s or %s) to authenticate as a GitHub App", EnvAppID, EnvAppIDAlias)
	}
	appID, err := strconv.ParseInt(strings.TrimSpace(appIDRaw), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be a numeric GitHub App ID, got %q", appIDSource, appIDRaw)
	}
	if appID <= 0 {
		return nil, fmt.Errorf("%s must be a positive GitHub App ID, got %d", appIDSource, appID)
	}

	if apiURL != "" {
		if err := validateAPIURL(apiURL); err != nil {
			return nil, err
		}
	}

	privateKey, keySource, err := loadPrivateKey(keyPath, keyInline, keyInlineSource)
	if err != nil {
		return nil, err
	}

	return &Config{
		AppID:            appID,
		PrivateKey:       privateKey,
		APIBaseURL:       apiURL,
		AppIDSource:      appIDSource,
		PrivateKeySource: keySource,
	}, nil
}

// lookupEnv returns the value and name of the first environment variable among
// names that is non-empty after trimming surrounding whitespace. The value is
// returned untrimmed so exact contents (such as a PEM key) are preserved. When
// none is set, it returns empty strings.
func lookupEnv(names ...string) (value, source string) {
	for _, name := range names {
		if raw := os.Getenv(name); strings.TrimSpace(raw) != "" {
			return raw, name
		}
	}
	return "", ""
}

// loadPrivateKey resolves the private key, preferring the file path over the
// inline value. When a path is set but cannot be read, it fails rather than
// silently falling back to the inline value. It returns the name of the
// environment variable that supplied the key, for diagnostics.
func loadPrivateKey(path, inline, inlineSource string) ([]byte, string, error) {
	if path != "" {
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("reading %s (%q): %w", EnvPrivateKeyPath, path, err)
		}
		if strings.TrimSpace(string(key)) == "" {
			return nil, "", fmt.Errorf("%s (%q) is empty", EnvPrivateKeyPath, path)
		}
		return key, EnvPrivateKeyPath, nil
	}
	if strings.TrimSpace(inline) != "" {
		return []byte(inline), inlineSource, nil
	}
	return nil, "", fmt.Errorf("a private key is required: set %s (preferred), %s, or %s", EnvPrivateKeyPath, EnvPrivateKey, EnvPrivateKeyAlias)
}

// validateAPIURL ensures a caller-supplied API base URL is safe to send
// credentials to: an absolute HTTPS URL with a host and no userinfo, query, or
// fragment. Requiring HTTPS prevents leaking app JWTs or installation tokens
// over plaintext.
func validateAPIURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", EnvAPIURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%s must be an https URL, got scheme %q", EnvAPIURL, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%s must include a host", EnvAPIURL)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s must not include userinfo, a query, or a fragment", EnvAPIURL)
	}
	return nil
}
