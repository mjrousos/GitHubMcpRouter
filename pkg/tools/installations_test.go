package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/githubapp"
)

// stubLister is a test double for InstallationLister.
type stubLister struct {
	insts []githubapp.Installation
	err   error
}

func (s stubLister) Installations(context.Context) ([]githubapp.Installation, error) {
	return s.insts, s.err
}

// newInstallationsClient registers the list_installations tool backed by the
// given lister and returns a connected client session.
func newInstallationsClient(t *testing.T, lister InstallationLister) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	AddListInstallations(server, lister)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession, ctx
}

func TestListInstallations_Success(t *testing.T) {
	lister := stubLister{insts: []githubapp.Installation{
		{ID: 456, Account: "octo-org", AccountType: "Organization", RepositorySelection: "all"},
		{ID: 321, Account: "octocat", AccountType: "User", RepositorySelection: "selected"},
	}}
	cs, ctx := newInstallationsClient(t, lister)

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_installations",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}

	text := textContent(t, res)
	for _, want := range []string{"2 installation(s)", "456", "octo-org", "321", "octocat"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}

func TestListInstallations_Error(t *testing.T) {
	cs, ctx := newInstallationsClient(t, stubLister{err: errors.New("boom")})

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_installations",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned a protocol error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected an error result when the lister fails, got: %+v", res.Content)
	}
}

func textContent(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	return tc.Text
}
