# GitHubMcpRouter

A minimal [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server
written in Go. It communicates exclusively over **stdio** and currently exposes a
single `echo` tool. This is an early scaffold intended to grow over time.

The project structure follows the conventions of
[github/github-mcp-server](https://github.com/github/github-mcp-server).

## Layout

```
cmd/mcp-router/      CLI entrypoint (cobra); defines the `stdio` subcommand
internal/server/     Server construction and the stdio run loop
internal/githubapp/  GitHub App authentication (JWT + installation tokens)
pkg/tools/           MCP tool definitions (currently just `echo`)
```

## Requirements

- Go 1.25 or newer

## Build

```sh
go build -o bin/mcp-router ./cmd/mcp-router
```

## Run

The server speaks JSON-RPC over standard input/output:

```sh
./bin/mcp-router stdio
```

It is meant to be launched by an MCP host (editor, agent, etc.) rather than used
interactively. Example host configuration:

```json
{
  "servers": {
    "mcp-router": {
      "command": "/path/to/bin/mcp-router",
      "args": ["stdio"]
    }
  }
}
```

## Authentication

The server authenticates with GitHub as a **GitHub App**, server-to-server. On
startup it reads the app's credentials from the environment; at request time it
resolves the relevant **installation** dynamically (by repository, organization,
or user) and mints a short-lived installation access token (valid one hour,
refreshed automatically).

| Variable | Required | Description |
| --- | --- | --- |
| `GITHUB_APP_ID` | yes | Numeric GitHub App ID (used as the JWT issuer). |
| `GITHUB_APP_PRIVATE_KEY_PATH` | one of the two | Path to the app's PEM private key. **Preferred.** |
| `GITHUB_APP_PRIVATE_KEY` | one of the two | The PEM private key contents (used only if `_PATH` is unset). |
| `GITHUB_API_URL` | no | API base URL for GitHub Enterprise Server; defaults to the public API. |

Behavior:

- If none of these variables are set, the server still runs (GitHub-backed tools
  are simply unavailable) — handy for the `echo` demo below.
- If they are set, the private key is parsed and validated at startup, so a bad
  key fails fast with a clear error.
- Store the private key securely (a key vault or a secret store). Never commit it.

## Tools

| Tool | Arguments | Description |
| --- | --- | --- |
| `echo` | `text` (string) | Returns the text upper-cased and prefixed `Message: `. |
| `list_installations` | none | Lists the GitHub App's installations (ID + account). Requires GitHub App credentials. |

`list_installations` is only registered when GitHub App credentials are
configured (see [Authentication](#authentication)); `echo` is always available.

## Try it without an MCP host

The server speaks JSON-RPC, so you can drive it by hand. Every session must begin
with an `initialize` request followed by an `initialized` notification before any
tool call. The [`test/echo-demo.jsonl`](test/echo-demo.jsonl) file contains
exactly that sequence:

```jsonl
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"manual","version":"1.0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello from stdin"}}}
```

Timing matters: the server shuts down as soon as stdin reaches EOF, so if you dump
all three lines at once and close the pipe, it may exit before answering the tool
call. The messages need to be delivered with a brief gap (and stdin kept open long
enough for the reply).

The easiest reliable way on Windows is the helper script, which builds the binary
if needed, sends each line with a small pause, and prints the responses:

```powershell
.\test\echo-demo.ps1
```

On Unix, a shell pipe works because the shell streams stdin; the trailing `sleep`
keeps the pipe open until the server has replied:

```sh
go build -o bin/mcp-router ./cmd/mcp-router
{ cat test/echo-demo.jsonl; sleep 1; } | ./bin/mcp-router stdio
```

Either way you should see two JSON responses on stdout (the notification gets
none); the second one holds the transformed text:

```json
{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"Message: HELLO FROM STDIN"}]}}
```

## Development

```sh
go build ./...
go vet ./...
```

## Adding a tool

1. Define the input struct and an `Add<Name>` function in a new file under
   `pkg/tools/`.
2. Register it from `internal/server/server.go` in `New`.
