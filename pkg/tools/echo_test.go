package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newEchoClient spins up an in-memory server exposing only the echo tool and
// returns a connected, initialized client session. Both sessions are closed
// automatically when the test finishes.
func newEchoClient(t *testing.T) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	AddEcho(server)

	// Per the SDK contract the server must be connected before the client, since
	// the client initializes the session during connection.
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

// callEcho calls the echo tool and returns the single text content it produces,
// failing the test on any protocol or tool error.
func callEcho(t *testing.T, cs *mcp.ClientSession, ctx context.Context, text string) string {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": text},
	})
	if err != nil {
		t.Fatalf("CallTool(echo): %v", err)
	}
	if res.IsError {
		t.Fatalf("echo returned tool error: %+v", res.Content)
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected exactly 1 content item, got %d: %+v", len(res.Content), res.Content)
	}
	text0, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	return text0.Text
}

func TestEchoTransformsInput(t *testing.T) {
	cs, ctx := newEchoClient(t)

	cases := []struct {
		name string
		text string
		want string
	}{
		{"simple", "hello world", "Message: HELLO WORLD"},
		{"empty", "", "Message: "},
		{"already_upper", "HELLO", "Message: HELLO"},
		{"mixed_case", "HeLLo", "Message: HELLO"},
		{"digits_and_punct", "a1-b2!?", "Message: A1-B2!?"},
		{"multiline", "line1\nline2", "Message: LINE1\nLINE2"},
		{"accented", "café", "Message: CAFÉ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := callEcho(t, cs, ctx, tc.text); got != tc.want {
				t.Errorf("echo mismatch:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestEchoIsListedWithSchema(t *testing.T) {
	cs, ctx := newEchoClient(t)

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	var echo *mcp.Tool
	for _, tool := range res.Tools {
		if tool.Name == "echo" {
			echo = tool
			break
		}
	}
	if echo == nil {
		t.Fatal("echo tool not present in tools/list")
	}
	if echo.Description == "" {
		t.Error("echo tool is missing a description")
	}

	// The client sees the input schema as a plain map[string]any. It should
	// declare a string "text" property and mark it required.
	schema, ok := echo.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("input schema has unexpected type %T", echo.InputSchema)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || props["text"] == nil {
		t.Errorf("input schema does not declare a 'text' property: %v", schema)
	}
	if !contains(schema["required"], "text") {
		t.Errorf("input schema does not mark 'text' as required: %v", schema["required"])
	}
}

func TestEchoRejectsMissingText(t *testing.T) {
	cs, ctx := newEchoClient(t)

	// "text" is required, so an empty argument object must be rejected. The SDK
	// may surface this either as a protocol error or as an error result.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{},
	})
	if err != nil {
		return // acceptable: reported as a protocol-level error
	}
	if !res.IsError {
		t.Errorf("expected an error for missing 'text' argument, got: %+v", res.Content)
	}
}

// contains reports whether the JSON-decoded schema list (an []any) holds want.
func contains(list any, want string) bool {
	items, ok := list.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if s, ok := item.(string); ok && s == want {
			return true
		}
	}
	return false
}
