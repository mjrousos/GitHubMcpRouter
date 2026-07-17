# GitHubMcpRouter

A [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server written
in Go. It communicates exclusively over **stdio**, authenticates to GitHub as a
**GitHub App**, and acts as a multi-organization **router** in front of the
official [github/github-mcp-server](https://github.com/github/github-mcp-server):
it runs one `github-mcp-server` child process per installation and delegates each
tool call to the right one (routing by owner, or fanning out across all orgs).

The project structure follows the conventions of
[github/github-mcp-server](https://github.com/github/github-mcp-server).

## Layout

```
cmd/mcp-router/      CLI entrypoint (cobra); defines the `stdio` subcommand
internal/server/     Server construction and the stdio run loop
internal/githubapp/  GitHub App authentication (JWT + installation tokens)
internal/downstream/ Per-org github-mcp-server process pool + routing
pkg/tools/           MCP tool definitions
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
| `GITHUB_API_URL` | no | API base URL for GitHub Enterprise Server (must be an absolute `https` URL); defaults to the public API. |

Behavior:

- If none of these variables are set, the server still runs (GitHub-backed tools
  are simply unavailable) — handy for the `echo` demo below.
- If they are set, the private key is parsed and validated at startup, so a bad
  key fails fast with a clear error.
- Store the private key securely (a key vault or a secret store). Never commit it.

## Tools

| Tool | Arguments | Routing | Description |
| --- | --- | --- | --- |
| `echo` | `text` (string) | — | Returns the text upper-cased and prefixed `Message: `. |
| `list_installations` | none | — | Lists the GitHub App's installations (ID + account). |
| `get_file_contents` | `owner`\*, `repo`\*, `path`, `ref`, `sha` | by `owner` | Get a file/directory from a repo, routed to the owner's org. |
| `search_code` | `query`\*, `sort`, `order`, `page`, `perPage` | fan-out | Search code across **all** connected orgs; results are merged. |

`echo` is always available — it's a simple, credential-free liveness check.
`list_installations` requires GitHub App credentials. `get_file_contents` and
`search_code` additionally require the `github-mcp-server` binary (see
[Multi-organization routing](#multi-organization-routing)). Their input schemas
mirror the identically named tools in the official `github-mcp-server`.

## Multi-organization routing

The official `github-mcp-server` authenticates as a **single** installation. To
work across many organizations, this server runs one `github-mcp-server` child per
installation and delegates:

- **Owner-scoped tools** (e.g. `get_file_contents`) route to the child for the
  request's `owner`.
- **Non-owner-scoped tools** (e.g. `search_code`) fan out to every connected
  child and the results are combined (best-effort: a failing org is reported but
  doesn't fail the whole call).

Children are spawned lazily, cached for the server's lifetime, and shut down on
exit (a child that exits is evicted and re-spawned on the next call). Each child
inherits this server's `GITHUB_APP_ID` / private-key env and receives its own
`GITHUB_APP_INSTALLATION_ID`. Conflicting credentials
(`GITHUB_PERSONAL_ACCESS_TOKEN`, `GITHUB_TOKEN`) are stripped so children always
authenticate as the installation, and for GitHub Enterprise the child's
`GITHUB_HOST` is derived from `GITHUB_API_URL`.

Install the GitHub App-capable `github-mcp-server` and make sure it is on `PATH`
(or point `GITHUB_MCP_SERVER_PATH` at it):

```sh
go install github.com/github/github-mcp-server/cmd/github-mcp-server@sammorrowdrums-github-app-s2s-auth
```

| Variable | Required | Description |
| --- | --- | --- |
| `GITHUB_MCP_SERVER_PATH` | no | Path to the `github-mcp-server` binary; defaults to looking it up on `PATH`. |
| `GITHUB_APP_ALLOWED_ORGS` | no | Comma-separated org/user logins to delegate to. When unset (blank), every installation of the app is used; a non-blank but empty value (e.g. `,`) allows none. |

If the binary can't be found, the routing tools are skipped (with a note on
stderr) and the server still runs its other tools.

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

This exercises `echo` only, so it needs no GitHub credentials — it's a quick way
to confirm the server starts and speaks MCP. The GitHub tools require GitHub App
credentials and the `github-mcp-server` binary as described above.

## Development

```sh
go build ./...
go vet ./...
go test ./...
```

## Adding a tool

**A simple, self-contained tool** (like `echo`):

1. Define the input struct and an `Add<Name>(server *mcp.Server)` function in a
   new file under `pkg/tools/`.
2. Register it from `internal/server/server.go` in `New`.

**A GitHub tool that delegates to `github-mcp-server`** (like `get_file_contents`
or `search_code`):

1. Add an `Add<Name>(server, router)` function in `pkg/tools/` whose input
   schema mirrors the identically named tool in the official `github-mcp-server`,
   and forward the raw arguments to a downstream child.
   - Owner-scoped tools resolve the child with `Router.ClientForOwner(owner)`.
   - Non-owner-scoped tools fan out with `Router.AllClients()` and merge the
     results.
2. Register it from `internal/server/server.go` in `New`, guarded by
   `cfg.Router != nil` so it only appears when routing is available.
