// Command mcp-router is the entrypoint for the multi-organization GitHub MCP
// router server.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mjrousos/GitHubMcpRouter/internal/server"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:   "mcp-router",
		Short: "Multi-organization GitHub MCP router",
		Long: "An MCP server that routes GitHub tool calls across multiple organizations.\n\n" +
			"When configured with GitHub App credentials, it wraps the official github-mcp-server:\n" +
			"one github-mcp-server child process is run per installation, and each tool call is\n" +
			"delegated to the right one (routed by owner, or fanned out across all organizations).\n" +
			"Without credentials it still runs, exposing only the tools that don't need GitHub access.\n" +
			"Communicates over stdio.",
		Version: version,
		// Don't print usage text when a command returns a runtime error
		// (e.g. the transport closing); usage is only helpful for bad input.
		SilenceUsage: true,
	}

	stdioCmd := &cobra.Command{
		Use:   "stdio",
		Short: "Start the MCP server over stdio",
		Long:  "Start a server that communicates via standard input/output streams using JSON-RPC messages.",
		RunE: func(_ *cobra.Command, _ []string) error {
			return server.RunStdio(server.Config{Version: version})
		},
	}

	rootCmd.AddCommand(stdioCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
