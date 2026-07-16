package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/githubapp"
)

// InstallationLister lists the installations available to the GitHub App. It is
// satisfied by *githubapp.Authenticator and kept as an interface so the tool can
// be tested without a live GitHub App.
type InstallationLister interface {
	Installations(ctx context.Context) ([]githubapp.Installation, error)
}

// ListInstallationsInput has no parameters; the tool lists every installation.
type ListInstallationsInput struct{}

// AddListInstallations registers the "list_installations" tool, which reports
// the installations of the GitHub App — each installation's ID and the account
// it is installed on. Only the app ID and private key are needed; installation
// IDs are discovered at call time.
func AddListInstallations(server *mcp.Server, lister InstallationLister) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_installations",
		Description: "Lists the installations of the GitHub App, including each installation's ID and the account (organization or user) it is installed on.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInstallationsInput) (*mcp.CallToolResult, any, error) {
		installations, err := lister.Installations(ctx)
		if err != nil {
			return nil, nil, err
		}
		data, err := json.MarshalIndent(installations, "", "  ")
		if err != nil {
			return nil, nil, fmt.Errorf("encoding installations: %w", err)
		}
		text := fmt.Sprintf("%d installation(s):\n%s", len(installations), data)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})
}
