package downstream

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectTimeout bounds how long we wait for a child to complete the MCP
// initialize handshake.
const connectTimeout = 30 * time.Second

// spawner starts child github-mcp-server processes and connects to them as an
// MCP client over stdio.
type spawner struct {
	ctx        context.Context // lifetime of all children; cancelled on Router.Close
	binaryPath string
	baseEnv    []string
	clientInfo *mcp.Implementation
}

// connect starts a child process for the installation and returns a connected
// client session (which acts as both the ToolCaller and its closer).
func (s *spawner) connect(installationID int64) (ToolCaller, func() error, error) {
	cmd := exec.CommandContext(s.ctx, s.binaryPath, "stdio")
	cmd.Env = append(childEnv(s.baseEnv), fmt.Sprintf("%s=%d", envInstallationID, installationID))
	cmd.Stderr = os.Stderr

	connectCtx, cancel := context.WithTimeout(s.ctx, connectTimeout)
	defer cancel()

	client := mcp.NewClient(s.clientInfo, nil)
	session, err := client.Connect(connectCtx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		// Connect may have started the process before failing; make sure it does
		// not linger.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, nil, err
	}
	return session, session.Close, nil
}

// childEnv returns a fresh copy of the base environment with any existing
// GITHUB_APP_INSTALLATION_ID removed, so the per-installation value we append is
// the only one that takes effect.
func childEnv(baseEnv []string) []string {
	out := make([]string, 0, len(baseEnv)+1)
	prefix := envInstallationID + "="
	for _, kv := range baseEnv {
		if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
			continue
		}
		out = append(out, kv)
	}
	return out
}
