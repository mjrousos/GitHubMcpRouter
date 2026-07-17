package tools

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/downstream"
)

func TestGetFileContents_RoutesByOwnerAndForwards(t *testing.T) {
	octo := &recordingCaller{result: textResult("contents from octo-org")}
	router := fakeOwnerRouter{callers: map[string]downstream.ToolCaller{
		"octo-org": octo,
	}}
	cs, ctx := newToolSession(t, func(s *mcp.Server) { AddGetFileContents(s, router) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_file_contents",
		// Owner deliberately differs in case from the routing key.
		Arguments: map[string]any{"owner": "Octo-Org", "repo": "hello", "path": "README.md"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res.Content)
	}
	if got := textContent(t, res); got != "contents from octo-org" {
		t.Errorf("result = %q, want the octo-org caller's result", got)
	}

	// The original arguments must be forwarded unchanged.
	var forwarded map[string]any
	if err := json.Unmarshal(octo.lastArgs, &forwarded); err != nil {
		t.Fatalf("forwarded args not valid JSON: %v", err)
	}
	if forwarded["repo"] != "hello" || forwarded["path"] != "README.md" {
		t.Errorf("forwarded args = %v, want repo=hello path=README.md", forwarded)
	}
}

func TestGetFileContents_UnknownOwner(t *testing.T) {
	router := fakeOwnerRouter{callers: map[string]downstream.ToolCaller{}}
	cs, ctx := newToolSession(t, func(s *mcp.Server) { AddGetFileContents(s, router) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_file_contents",
		Arguments: map[string]any{"owner": "ghost", "repo": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected an error result for an unknown owner, got %+v", res.Content)
	}
}

func TestGetFileContents_MissingOwner(t *testing.T) {
	router := fakeOwnerRouter{callers: map[string]downstream.ToolCaller{}}
	cs, ctx := newToolSession(t, func(s *mcp.Server) { AddGetFileContents(s, router) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_file_contents",
		Arguments: map[string]any{"repo": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result when owner is missing")
	}
	if got := textContent(t, res); got == "" {
		t.Error("expected a message explaining owner is required")
	}
}
