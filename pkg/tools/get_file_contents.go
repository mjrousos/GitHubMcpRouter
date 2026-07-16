package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddGetFileContents registers the "get_file_contents" tool. It routes to the
// downstream github-mcp-server for the request's owner and forwards the call
// unchanged, returning the downstream result verbatim.
//
// The input schema mirrors the official github-mcp-server tool of the same name.
func AddGetFileContents(server *mcp.Server, router ownerRouter) {
	server.AddTool(&mcp.Tool{
		Name:        "get_file_contents",
		Description: "Get the contents of a file or directory from a GitHub repository. Routed to the correct organization by owner.",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"owner": {Type: "string", Description: "Repository owner (username or organization)"},
				"repo":  {Type: "string", Description: "Repository name"},
				"path":  {Type: "string", Description: "Path to file/directory", Default: json.RawMessage(`"/"`)},
				"ref":   {Type: "string", Description: "Accepts optional git refs such as `refs/tags/{tag}`, `refs/heads/{branch}` or `refs/pull/{pr_number}/head`"},
				"sha":   {Type: "string", Description: "Accepts optional commit SHA. If specified, it will be used instead of ref"},
			},
			Required: []string{"owner", "repo"},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := rawArguments(req.Params.Arguments)

		var parsed struct {
			Owner string `json:"owner"`
		}
		if err := json.Unmarshal(args, &parsed); err != nil {
			return toolErrorf("invalid arguments: %v", err), nil
		}
		if strings.TrimSpace(parsed.Owner) == "" {
			return toolErrorf("owner is required"), nil
		}

		caller, err := router.ClientForOwner(ctx, parsed.Owner)
		if err != nil {
			return toolErrorf("%v", err), nil
		}

		res, err := caller.CallTool(ctx, &mcp.CallToolParams{
			Name:      "get_file_contents",
			Arguments: args,
		})
		if err != nil {
			return toolErrorf("get_file_contents failed for %q: %v", parsed.Owner, err), nil
		}
		return res, nil
	})
}
