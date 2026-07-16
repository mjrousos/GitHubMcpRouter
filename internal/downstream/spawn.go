package downstream

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectTimeout bounds how long we wait for a child to start and complete the
// MCP initialize handshake.
const connectTimeout = 30 * time.Second

// childAuthConflicts are environment variables that would make the child
// authenticate as something other than the intended GitHub App installation
// (e.g. a personal access token). They are stripped from the child environment.
var childAuthConflicts = []string{
	envInstallationID, // set per-installation below
	"GITHUB_PERSONAL_ACCESS_TOKEN",
	"GITHUB_TOKEN",
}

// spawner starts child github-mcp-server processes and connects to them as an
// MCP client over stdio.
type spawner struct {
	ctx        context.Context // lifetime of all children; cancelled on Router.Close
	binaryPath string
	baseEnv    []string
	clientInfo *mcp.Implementation
}

// connect starts a child process for the installation and returns a connected
// session. The child is bound to the spawner's lifetime context; the handshake
// additionally respects the caller's context and a timeout.
func (s *spawner) connect(ctx context.Context, installationID int64) (downstreamSession, error) {
	cmd := exec.CommandContext(s.ctx, s.binaryPath, "stdio")
	cmd.Env = childEnv(s.baseEnv, installationID)
	cmd.Stderr = os.Stderr

	handshakeCtx, cancel := context.WithTimeout(s.ctx, connectTimeout)
	defer cancel()
	// Cancel the handshake if the caller gives up.
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	client := mcp.NewClient(s.clientInfo, nil)
	session, err := client.Connect(handshakeCtx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		// Connect may have started the process before failing; make sure it
		// does not linger.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, err
	}
	return session, nil
}

// childEnv builds the environment for a child authenticating as the given
// installation. It starts from the base environment, drops variables that would
// conflict with GitHub App installation auth, sets GITHUB_APP_INSTALLATION_ID,
// and — for GitHub Enterprise — derives GITHUB_HOST from GITHUB_API_URL (the
// child uses GITHUB_HOST, not GITHUB_API_URL).
func childEnv(baseEnv []string, installationID int64) []string {
	var apiURL, host string
	out := make([]string, 0, len(baseEnv)+2)
	for _, kv := range baseEnv {
		key, val, _ := strings.Cut(kv, "=")
		if isChildAuthConflict(key) {
			continue
		}
		switch key {
		case "GITHUB_API_URL":
			apiURL = val
		case "GITHUB_HOST":
			host = val
		}
		out = append(out, kv)
	}

	out = append(out, envInstallationID+"="+itoa(installationID))
	if host == "" && apiURL != "" {
		if h := hostFromAPIURL(apiURL); h != "" {
			out = append(out, "GITHUB_HOST="+h)
		}
	}
	return out
}

func isChildAuthConflict(key string) bool {
	for _, c := range childAuthConflicts {
		if key == c {
			return true
		}
	}
	return false
}

// hostFromAPIURL reduces a REST API URL (e.g. https://ghe.example.com/api/v3) to
// the host form github-mcp-server expects for GITHUB_HOST (https://ghe.example.com).
func hostFromAPIURL(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
