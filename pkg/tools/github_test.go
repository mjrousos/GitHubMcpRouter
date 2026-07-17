package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/downstream"
)

// newToolSession registers tools via register and returns a connected client.
func newToolSession(t *testing.T, register func(*mcp.Server)) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	register(server)

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

// recordingCaller records the arguments it was forwarded and returns a canned
// result/error.
type recordingCaller struct {
	result   *mcp.CallToolResult
	err      error
	lastArgs json.RawMessage
}

func (c *recordingCaller) CallTool(_ context.Context, p *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if raw, ok := p.Arguments.(json.RawMessage); ok {
		c.lastArgs = raw
	}
	return c.result, c.err
}

// fakeOwnerRouter routes owners to callers for get_file_contents tests.
type fakeOwnerRouter struct {
	callers map[string]downstream.ToolCaller // lowercased owner -> caller
}

func (r fakeOwnerRouter) ClientForOwner(_ context.Context, owner string) (downstream.ToolCaller, error) {
	if c, ok := r.callers[strings.ToLower(owner)]; ok {
		return c, nil
	}
	return nil, &ownerNotFoundError{owner}
}

type ownerNotFoundError struct{ owner string }

func (e *ownerNotFoundError) Error() string { return "no installation for " + e.owner }

// fakeFanoutRouter returns a fixed set of clients for search_code tests.
type fakeFanoutRouter struct {
	clients []downstream.OwnerClient
	err     error
}

func (r fakeFanoutRouter) AllClients(context.Context) ([]downstream.OwnerClient, error) {
	return r.clients, r.err
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
