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
			"When configured with GitHub App credentials and the github-mcp-server binary, it wraps\n" +
			"that server: one github-mcp-server child process is run per installation, and each tool\n" +
			"call is delegated to the right one (routed by owner, or fanned out across all\n" +
			"organizations). Without them it still runs, exposing only the tools that don't need\n" +
			"GitHub routing. Communicates over stdio or HTTP (see the stdio and http subcommands).",
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

	httpCmd := &cobra.Command{
		Use:   "http",
		Short: "Start the MCP server over HTTP",
		Long: "Start a server that communicates over the streamable HTTP transport.\n\n" +
			"The MCP endpoint is served at " + server.MCPPath + " and a health check at /healthz.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			address, err := cmd.Flags().GetString("address")
			if err != nil {
				return err
			}
			return server.RunHTTP(server.Config{Version: version}, server.HTTPConfig{Address: address})
		},
	}
	httpCmd.Flags().String("address", server.DefaultHTTPAddress, "TCP address to listen on (host:port)")

	rootCmd.AddCommand(stdioCmd)
	rootCmd.AddCommand(httpCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
