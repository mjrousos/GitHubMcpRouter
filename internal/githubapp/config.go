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
const (
	EnvAppID          = "GITHUB_APP_ID"
	EnvPrivateKeyPath = "GITHUB_APP_PRIVATE_KEY_PATH"
	EnvPrivateKey     = "GITHUB_APP_PRIVATE_KEY"
	EnvAPIURL         = "GITHUB_API_URL"
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
}

// LoadConfigFromEnv reads GitHub App configuration from environment variables.
//
// It returns (nil, nil) when none of the GitHub App variables are set, so the
// server can run without GitHub access. It returns an error when configuration
// is partially present or invalid.
//
// The private key is read from GITHUB_APP_PRIVATE_KEY_PATH (preferred) or, if
// that is unset, from GITHUB_APP_PRIVATE_KEY.
func LoadConfigFromEnv() (*Config, error) {
	appIDRaw := strings.TrimSpace(os.Getenv(EnvAppID))
	keyPath := strings.TrimSpace(os.Getenv(EnvPrivateKeyPath))
	keyInline := os.Getenv(EnvPrivateKey)
	apiURL := strings.TrimSpace(os.Getenv(EnvAPIURL))

	// Nothing configured at all: GitHub App auth is simply disabled.
	if appIDRaw == "" && keyPath == "" && strings.TrimSpace(keyInline) == "" {
		return nil, nil
	}

	if appIDRaw == "" {
		return nil, fmt.Errorf("%s must be set to authenticate as a GitHub App", EnvAppID)
	}
	appID, err := strconv.ParseInt(appIDRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be a numeric GitHub App ID, got %q", EnvAppID, appIDRaw)
	}
	if appID <= 0 {
		return nil, fmt.Errorf("%s must be a positive GitHub App ID, got %d", EnvAppID, appID)
	}

	if apiURL != "" {
		if err := validateAPIURL(apiURL); err != nil {
			return nil, err
		}
	}

	privateKey, err := loadPrivateKey(keyPath, keyInline)
	if err != nil {
		return nil, err
	}

	return &Config{
		AppID:      appID,
		PrivateKey: privateKey,
		APIBaseURL: apiURL,
	}, nil
}

// loadPrivateKey resolves the private key, preferring the file path over the
// inline value. When a path is set but cannot be read, it fails rather than
// silently falling back to the inline value.
func loadPrivateKey(path, inline string) ([]byte, error) {
	if path != "" {
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s (%q): %w", EnvPrivateKeyPath, path, err)
		}
		if strings.TrimSpace(string(key)) == "" {
			return nil, fmt.Errorf("%s (%q) is empty", EnvPrivateKeyPath, path)
		}
		return key, nil
	}
	if strings.TrimSpace(inline) != "" {
		return []byte(inline), nil
	}
	return nil, fmt.Errorf("a private key is required: set %s (preferred) or %s", EnvPrivateKeyPath, EnvPrivateKey)
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
