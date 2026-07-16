// Package downstream manages child github-mcp-server processes and routes MCP
// tool calls to the right one.
//
// The official github-mcp-server authenticates as a single GitHub App
// installation. To work across multiple organizations, this package runs one
// child github-mcp-server process per installation and delegates each tool call
// to the appropriate child — routing by owner, or fanning out to every child
// when a tool is not owner-scoped.
package downstream

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Environment variables that configure routing.
const (
	// EnvAllowedOrgs optionally restricts which organizations/users this server
	// will delegate to (comma-separated logins). When empty, every installation
	// of the GitHub App is allowed.
	EnvAllowedOrgs = "GITHUB_APP_ALLOWED_ORGS"

	// EnvServerPath optionally overrides the path to the github-mcp-server
	// binary. When empty, the binary is looked up on PATH.
	EnvServerPath = "GITHUB_MCP_SERVER_PATH"

	// envInstallationID is set per child process to select its installation.
	envInstallationID = "GITHUB_APP_INSTALLATION_ID"

	defaultBinaryName = "github-mcp-server"
)

// parseAllowedOrgs parses a comma-separated allow-list into a set of lowercased
// logins.
//
//   - A blank value returns nil, meaning "not configured" (all installations
//     are allowed).
//   - A non-blank value returns a (possibly empty) set. An empty set — e.g. from
//     a malformed value like "," — allows nothing, so a configuration typo fails
//     closed rather than silently allowing every organization.
func parseAllowedOrgs(raw string) map[string]struct{} {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	set := make(map[string]struct{})
	for _, field := range strings.Split(raw, ",") {
		if login := strings.ToLower(strings.TrimSpace(field)); login != "" {
			set[login] = struct{}{}
		}
	}
	return set
}

// ResolveBinary returns the path to the github-mcp-server executable, honoring
// GITHUB_MCP_SERVER_PATH and otherwise looking it up on PATH.
func ResolveBinary() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvServerPath)); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s=%q: %w", EnvServerPath, p, err)
		}
		return p, nil
	}
	path, err := exec.LookPath(defaultBinaryName)
	if err != nil {
		return "", fmt.Errorf("could not find %q on PATH (set %s to override): %w", defaultBinaryName, EnvServerPath, err)
	}
	return path, nil
}
