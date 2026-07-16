package server

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newClientFor connects an in-memory client to a server built by New with the
// given config, returning the initialized client session. Both sessions are
// closed automatically when the test finishes.
func newClientFor(t *testing.T, cfg Config) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()

	srv := New(cfg)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
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

func TestNewRegistersEchoTool(t *testing.T) {
	cs, ctx := newClientFor(t, Config{Version: "test"})

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}

	if len(names) != 1 || names[0] != "echo" {
		t.Errorf("expected exactly one tool named \"echo\", got %v", names)
	}
}

func TestNewReportsServerInfo(t *testing.T) {
	cs, _ := newClientFor(t, Config{Version: "1.2.3-test"})

	info := cs.InitializeResult().ServerInfo
	if info == nil {
		t.Fatal("server did not report ServerInfo")
	}
	if info.Name != "mcp-router" {
		t.Errorf("server name: got %q, want %q", info.Name, "mcp-router")
	}
	if info.Version != "1.2.3-test" {
		t.Errorf("server version: got %q, want %q", info.Version, "1.2.3-test")
	}
}

func TestNewEchoEndToEnd(t *testing.T) {
	cs, ctx := newClientFor(t, Config{Version: "test"})

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "round trip"},
	})
	if err != nil {
		t.Fatalf("CallTool(echo): %v", err)
	}
	if res.IsError {
		t.Fatalf("echo returned tool error: %+v", res.Content)
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(res.Content))
	}
	got, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	if want := "Message: ROUND TRIP"; got.Text != want {
		t.Errorf("echo mismatch: got %q, want %q", got.Text, want)
	}
}
