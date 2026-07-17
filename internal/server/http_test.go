package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startTestHTTPServer builds the production HTTP server wiring (via newHTTPServer)
// and serves it on a random localhost port, returning the base URL. It is torn
// down when the test finishes.
func startTestHTTPServer(t *testing.T, cfg Config) (baseURL string, cancel context.CancelFunc, served <-chan error) {
	t.Helper()
	ctx, cancelFn := context.WithCancel(context.Background())

	srv := newHTTPServer(ctx, cfg, HTTPConfig{})
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		cancelFn()
		t.Fatalf("listen: %v", err)
	}

	errCh := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		errCh <- serveHTTP(ctx, srv, ln)
		close(done)
	}()

	t.Cleanup(func() {
		cancelFn()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not shut down within 5s during cleanup")
		}
	})

	return "http://" + ln.Addr().String(), cancelFn, errCh
}

// connectHTTPClient connects an MCP client to the given base URL's MCP endpoint.
func connectHTTPClient(t *testing.T, baseURL string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: baseURL + MCPPath}, nil)
	if err != nil {
		t.Fatalf("connect over HTTP: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// TestHTTPTransportEndToEnd drives a full session (initialize, tools/list, echo)
// over the production HTTP wiring, hitting the real /mcp route.
func TestHTTPTransportEndToEnd(t *testing.T) {
	ctx := context.Background()
	baseURL, _, _ := startTestHTTPServer(t, Config{Version: "http-test"})

	session := connectHTTPClient(t, baseURL)

	if info := session.InitializeResult().ServerInfo; info == nil || info.Name != "mcp-router" {
		t.Fatalf("unexpected server info: %+v", info)
	}

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools over HTTP: %v", err)
	}
	var hasEcho bool
	for _, tool := range list.Tools {
		if tool.Name == "echo" {
			hasEcho = true
		}
	}
	if !hasEcho {
		t.Fatal("echo tool not advertised over HTTP")
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "over http"},
	})
	if err != nil {
		t.Fatalf("CallTool over HTTP: %v", err)
	}
	if res.IsError {
		t.Fatalf("echo returned an error: %+v", res.Content)
	}
	got, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	if want := "Message: OVER HTTP"; got.Text != want {
		t.Errorf("echo over HTTP = %q, want %q", got.Text, want)
	}
}

// TestHTTPHealthEndpoint verifies the /healthz check.
func TestHTTPHealthEndpoint(t *testing.T) {
	baseURL, _, _ := startTestHTTPServer(t, Config{Version: "http-test"})

	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
}

// TestHTTPGracefulShutdownWithConnectedClient asserts that cancelling the
// server context shuts the server down promptly even while a client holds an
// open session (with its standalone SSE stream). Without BaseContext tying
// request contexts to the server context, Shutdown would block until the
// timeout.
func TestHTTPGracefulShutdownWithConnectedClient(t *testing.T) {
	baseURL, cancel, served := startTestHTTPServer(t, Config{Version: "http-test"})

	// Establish a session; the client opens a standalone SSE stream by default.
	_ = connectHTTPClient(t, baseURL)

	start := time.Now()
	cancel()

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serveHTTP returned an error: %v", err)
		}
		if elapsed := time.Since(start); elapsed >= httpShutdownTimeout {
			t.Errorf("shutdown took %v (>= the %v timeout); it likely waited for the SSE stream", elapsed, httpShutdownTimeout)
		}
	case <-time.After(httpShutdownTimeout + 2*time.Second):
		t.Fatal("server did not shut down after context cancellation")
	}
}

// TestHTTPEndpointBuilt checks that newHTTPServer wires the expected address and
// a reachable /healthz without starting a listener.
func TestHTTPEndpointBuilt(t *testing.T) {
	srv := newHTTPServer(context.Background(), Config{Version: "test"}, HTTPConfig{Address: "localhost:12345"})
	if srv.Addr != "localhost:12345" {
		t.Errorf("Addr = %q, want localhost:12345", srv.Addr)
	}
	if srv.Handler == nil {
		t.Fatal("handler not set")
	}

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz via built handler = %d, want 200", rec.Code)
	}
}
