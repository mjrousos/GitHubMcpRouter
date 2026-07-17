// Command fakeserver is a minimal stand-in for github-mcp-server used by the
// downstream integration test. It speaks MCP over stdio and exposes
// get_file_contents and search_code tools that echo the installation ID it was
// started with (via GITHUB_APP_INSTALLATION_ID) so the test can verify routing.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	installationID := os.Getenv("GITHUB_APP_INSTALLATION_ID")

	server := mcp.NewServer(&mcp.Implementation{Name: "fake-github-mcp-server", Version: "test"}, nil)

	objectSchema := &jsonschema.Schema{Type: "object"}

	server.AddTool(&mcp.Tool{Name: "get_file_contents", InputSchema: objectSchema},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text := fmt.Sprintf("installation=%s args=%s", installationID, string(req.Params.Arguments))
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
		})

	server.AddTool(&mcp.Tool{Name: "search_code", InputSchema: objectSchema},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text := fmt.Sprintf(`{"total_count":1,"incomplete_results":false,"items":[{"installation":%q}]}`, installationID)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
		})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
