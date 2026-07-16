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

## Tools

| Tool   | Arguments       | Description                                            |
| ------ | --------------- | ------------------------------------------------------ |
| `echo` | `text` (string) | Returns the text upper-cased and prefixed `Message: `. |

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
