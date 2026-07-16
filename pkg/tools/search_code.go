package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/downstream"
)

// AddSearchCode registers the "search_code" tool. Because code search is not
// scoped to a single owner, it fans the call out to every connected downstream
// github-mcp-server and combines the results.
//
// The input schema mirrors the official github-mcp-server tool of the same name.
func AddSearchCode(server *mcp.Server, router fanoutRouter) {
	server.AddTool(&mcp.Tool{
		Name:        "search_code",
		Description: "Search for code across all connected GitHub organizations and combine the results. Note: because results are gathered from each organization independently, `sort`/`order` and `page`/`perPage` apply per-organization, not to the merged result.",
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"query":   {Type: "string", Description: "Search query using GitHub's powerful code search syntax (see the official get_file_contents/search_code docs)."},
				"sort":    {Type: "string", Description: "Sort field ('indexed' only)"},
				"order":   {Type: "string", Description: "Sort order for results", Enum: []any{"asc", "desc"}},
				"page":    {Type: "number", Description: "Page number for pagination (min 1)"},
				"perPage": {Type: "number", Description: "Results per page for pagination (min 1, max 100)"},
			},
			Required: []string{"query"},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := rawArguments(req.Params.Arguments)

		clients, startErr := router.AllClients(ctx)
		if len(clients) == 0 {
			if startErr != nil {
				return toolErrorf("no GitHub organizations available: %v", startErr), nil
			}
			return toolErrorf("no GitHub organizations available"), nil
		}

		outcomes := make([]searchOutcome, len(clients))
		var wg sync.WaitGroup
		for i, c := range clients {
			wg.Add(1)
			go func(i int, c downstream.OwnerClient) {
				defer wg.Done()
				res, err := c.Caller.CallTool(ctx, &mcp.CallToolParams{
					Name:      "search_code",
					Arguments: args,
				})
				outcomes[i] = searchOutcome{owner: c.Owner, res: res, err: err}
			}(i, c)
		}
		wg.Wait()

		return mergeSearchCode(outcomes, startErr), nil
	})
}

type searchOutcome struct {
	owner string
	res   *mcp.CallToolResult
	err   error
}

// codeSearchResult is the subset of GitHub's code search response shape needed
// to combine results across organizations.
type codeSearchResult struct {
	TotalCount        int               `json:"total_count"`
	IncompleteResults bool              `json:"incomplete_results"`
	Items             []json.RawMessage `json:"items"`
}

// mergeSearchCode combines per-organization results. It is best-effort: failed
// organizations are noted rather than failing the whole call, unless every
// organization failed.
func mergeSearchCode(outcomes []searchOutcome, startErr error) *mcp.CallToolResult {
	merged := codeSearchResult{Items: []json.RawMessage{}}
	var failures []string
	if startErr != nil {
		failures = append(failures, startErr.Error())
	}

	successes := 0
	for _, o := range outcomes {
		switch {
		case o.err != nil:
			failures = append(failures, fmt.Sprintf("%s: %v", o.owner, o.err))
		case o.res == nil:
			failures = append(failures, fmt.Sprintf("%s: empty response", o.owner))
		case o.res.IsError:
			failures = append(failures, fmt.Sprintf("%s: %s", o.owner, firstText(o.res)))
		default:
			var parsed codeSearchResult
			if err := json.Unmarshal([]byte(firstText(o.res)), &parsed); err != nil {
				failures = append(failures, fmt.Sprintf("%s: could not parse response: %v", o.owner, err))
				continue
			}
			merged.TotalCount += parsed.TotalCount
			merged.IncompleteResults = merged.IncompleteResults || parsed.IncompleteResults
			merged.Items = append(merged.Items, parsed.Items...)
			successes++
		}
	}

	if successes == 0 {
		return toolErrorf("search_code failed for all organizations:\n%s", strings.Join(failures, "\n"))
	}

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return toolErrorf("failed to encode combined results: %v", err)
	}
	content := []mcp.Content{&mcp.TextContent{Text: string(data)}}
	if len(failures) > 0 {
		content = append(content, &mcp.TextContent{Text: "Some organizations were skipped:\n" + strings.Join(failures, "\n")})
	}
	return &mcp.CallToolResult{Content: content}
}
