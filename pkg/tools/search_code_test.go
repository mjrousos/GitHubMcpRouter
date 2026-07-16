package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/downstream"
)

func codeSearchJSON(total int, incomplete bool, items ...string) string {
	return fmt.Sprintf(`{"total_count":%d,"incomplete_results":%t,"items":[%s]}`,
		total, incomplete, strings.Join(items, ","))
}

func TestSearchCode_FansOutAndMerges(t *testing.T) {
	octo := downstream.OwnerClient{
		Owner:  "octo-org",
		Caller: &recordingCaller{result: textResult(codeSearchJSON(2, false, `{"path":"a.go"}`, `{"path":"b.go"}`))},
	}
	acme := downstream.OwnerClient{
		Owner:  "acme",
		Caller: &recordingCaller{result: textResult(codeSearchJSON(1, true, `{"path":"c.go"}`))},
	}
	router := fakeFanoutRouter{clients: []downstream.OwnerClient{octo, acme}}
	cs, ctx := newToolSession(t, func(s *mcp.Server) { AddSearchCode(s, router) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_code",
		Arguments: map[string]any{"query": "func main"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res.Content)
	}

	var merged codeSearchResult
	if err := json.Unmarshal([]byte(textContent(t, res)), &merged); err != nil {
		t.Fatalf("combined result not valid JSON: %v", err)
	}
	if merged.TotalCount != 3 {
		t.Errorf("total_count = %d, want 3", merged.TotalCount)
	}
	if !merged.IncompleteResults {
		t.Errorf("incomplete_results = false, want true (OR of children)")
	}
	if len(merged.Items) != 3 {
		t.Errorf("items = %d, want 3", len(merged.Items))
	}
}

func TestSearchCode_PartialFailure(t *testing.T) {
	ok := downstream.OwnerClient{
		Owner:  "octo-org",
		Caller: &recordingCaller{result: textResult(codeSearchJSON(1, false, `{"path":"a.go"}`))},
	}
	bad := downstream.OwnerClient{
		Owner:  "acme",
		Caller: &recordingCaller{err: errors.New("boom")},
	}
	router := fakeFanoutRouter{clients: []downstream.OwnerClient{ok, bad}}
	cs, ctx := newToolSession(t, func(s *mcp.Server) { AddSearchCode(s, router) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_code",
		Arguments: map[string]any{"query": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("partial failure should not fail the whole call: %+v", res.Content)
	}
	// Primary content is the merged result (1 success); a second content notes
	// the skipped org.
	var merged codeSearchResult
	if err := json.Unmarshal([]byte(firstText(res)), &merged); err != nil {
		t.Fatalf("combined result not valid JSON: %v", err)
	}
	if merged.TotalCount != 1 {
		t.Errorf("total_count = %d, want 1 (only the successful org)", merged.TotalCount)
	}
	if len(res.Content) < 2 {
		t.Fatalf("expected a second content noting the skipped org")
	}
	note := res.Content[1].(*mcp.TextContent).Text
	if !strings.Contains(note, "acme") {
		t.Errorf("skip note = %q, want it to mention acme", note)
	}
}

func TestSearchCode_AllFail(t *testing.T) {
	bad1 := downstream.OwnerClient{Owner: "octo-org", Caller: &recordingCaller{err: errors.New("boom")}}
	bad2 := downstream.OwnerClient{Owner: "acme", Caller: &recordingCaller{result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "bad query"}}}}}
	router := fakeFanoutRouter{clients: []downstream.OwnerClient{bad1, bad2}}
	cs, ctx := newToolSession(t, func(s *mcp.Server) { AddSearchCode(s, router) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_code",
		Arguments: map[string]any{"query": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected an error result when every org fails")
	}
}

func TestSearchCode_NoInstallations(t *testing.T) {
	router := fakeFanoutRouter{clients: nil}
	cs, ctx := newToolSession(t, func(s *mcp.Server) { AddSearchCode(s, router) })

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_code",
		Arguments: map[string]any{"query": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected an error result when there are no installations")
	}
}
