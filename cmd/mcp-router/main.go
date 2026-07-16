// Command mcp-router is the entrypoint for the MCP Router server.
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
		Use:     "mcp-router",
		Short:   "MCP Router server",
		Long:    "An MCP server. Currently exposes a single echo tool and communicates over stdio.",
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
