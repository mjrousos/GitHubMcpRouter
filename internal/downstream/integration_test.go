package downstream

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestChildEnv_ReplacesInstallationID(t *testing.T) {
	base := []string{"GITHUB_APP_ID=1", "GITHUB_APP_INSTALLATION_ID=999", "PATH=/usr/bin"}
	got := childEnv(base)
	for _, kv := range got {
		if strings.HasPrefix(kv, "GITHUB_APP_INSTALLATION_ID=") {
			t.Errorf("childEnv should strip any inherited installation ID, found %q", kv)
		}
	}
	// The unrelated variables survive.
	if !contains(got, "GITHUB_APP_ID=1") || !contains(got, "PATH=/usr/bin") {
		t.Errorf("childEnv dropped unrelated variables: %v", got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// buildFakeServer compiles the testdata fake github-mcp-server and returns its
// path. The test is skipped when the Go toolchain is unavailable.
func buildFakeServer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	out := filepath.Join(t.TempDir(), "fakeserver")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./testdata/fakeserver")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building fake server: %v", err)
	}
	return out
}

// TestSpawnAndRoute_Integration exercises the real subprocess path: New builds a
// Router that spawns the (fake) github-mcp-server binary, and a routed call is
// forwarded to the child selected by installation ID.
func TestSpawnAndRoute_Integration(t *testing.T) {
	binary := buildFakeServer(t)

	lister := &fakeLister{}
	lister.set(inst(42, "octo-org"))

	router := New(Config{
		Lister:     lister,
		BinaryPath: binary,
		BaseEnv:    os.Environ(),
		Version:    "test",
	})
	t.Cleanup(func() { _ = router.Close() })

	caller, err := router.ClientForOwner(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("ClientForOwner: %v", err)
	}

	res, err := caller.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_file_contents",
		Arguments: json.RawMessage(`{"owner":"octo-org","repo":"hello"}`),
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "installation=42") {
		t.Errorf("routed to wrong installation; response = %q", text)
	}
	if !strings.Contains(text, `"repo":"hello"`) {
		t.Errorf("arguments were not forwarded; response = %q", text)
	}
}
