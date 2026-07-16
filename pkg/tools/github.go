package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/downstream"
)

// ownerRouter routes a tool call to the downstream github-mcp-server for a
// specific owner (organization or user). It is satisfied by *downstream.Router.
type ownerRouter interface {
	ClientForOwner(ctx context.Context, owner string) (downstream.ToolCaller, error)
}

// fanoutRouter exposes a caller for every allowed installation. It is satisfied
// by *downstream.Router.
type fanoutRouter interface {
	AllClients(ctx context.Context) ([]downstream.OwnerClient, error)
}

// toolErrorf builds an error tool result (IsError set) so the caller/LLM sees
// the failure rather than a protocol-level error.
func toolErrorf(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}
}

// firstText returns the text of the first TextContent in a result, or "".
func firstText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// rawArguments normalizes possibly-empty raw arguments to a valid JSON object.
func rawArguments(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}
