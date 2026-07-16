// Package tools contains the MCP tool definitions exposed by the server.
//
// Each tool is registered onto an *mcp.Server via a small Add* function so that
// the server wiring in internal/server stays declarative and tools can grow
// independently as the project evolves.
package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EchoInput describes the arguments accepted by the echo tool. Struct tags drive
// the generated JSON schema: `json` sets the argument name and `jsonschema`
// provides its description.
type EchoInput struct {
	Text string `json:"text" jsonschema:"the text to transform"`
}

// AddEcho registers the "echo" tool on the given server. The tool upper-cases the
// provided text and returns it prefixed with "Message: ".
func AddEcho(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Returns the given text converted to upper case and prefixed with \"Message: \".",
	}, handleEcho)
}

func handleEcho(_ context.Context, _ *mcp.CallToolRequest, input EchoInput) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Message: " + strings.ToUpper(input.Text)},
		},
	}, nil, nil
}
