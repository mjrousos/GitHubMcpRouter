package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestHTTPTransportEndToEnd serves the MCP server over the streamable HTTP
// handler (the same wiring RunHTTP uses) and drives a full session with the
// SDK's HTTP client transport.
func TestHTTPTransportEndToEnd(t *testing.T) {
	ctx := context.Background()

	srv := New(Config{Version: "http-test"})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return srv
	}, nil)

	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("connect over HTTP: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Server info comes back through the HTTP transport.
	if info := session.InitializeResult().ServerInfo; info == nil || info.Name != "mcp-router" {
		t.Fatalf("unexpected server info: %+v", info)
	}

	// tools/list works over HTTP.
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools over HTTP: %v", err)
	}
	var hasEcho bool
	for _, tool := range list.Tools {
		if tool.Name == "echo" {
			hasEcho = true
		}
	}
	if !hasEcho {
		t.Fatal("echo tool not advertised over HTTP")
	}

	// A tool call round-trips over HTTP.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "over http"},
	})
	if err != nil {
		t.Fatalf("CallTool over HTTP: %v", err)
	}
	if res.IsError {
		t.Fatalf("echo returned an error: %+v", res.Content)
	}
	got, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	if want := "Message: OVER HTTP"; got.Text != want {
		t.Errorf("echo over HTTP = %q, want %q", got.Text, want)
	}
}
