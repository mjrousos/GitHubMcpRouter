package downstream

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjrousos/GitHubMcpRouter/internal/githubapp"
)

// fakeLister is a test InstallationLister whose list can change between calls.
type fakeLister struct {
	mu    sync.Mutex
	insts []githubapp.Installation
	calls int
}

func (f *fakeLister) Installations(context.Context) ([]githubapp.Installation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return append([]githubapp.Installation(nil), f.insts...), nil
}

func (f *fakeLister) set(insts ...githubapp.Installation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.insts = insts
}

func (f *fakeLister) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeSession records calls and reports which installation handled them. It
// stays "alive" until Close is called, which unblocks Wait (mirroring
// *mcp.ClientSession semantics used by the manager's eviction watcher).
type fakeSession struct {
	installationID int64
	mu             sync.Mutex
	calls          int
	done           chan struct{}
	closeOnce      sync.Once
	onClose        func()
}

func newFakeSession(id int64) *fakeSession {
	return &fakeSession{installationID: id, done: make(chan struct{})}
}

func (c *fakeSession) CallTool(_ context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("installation=%d", c.installationID)}},
	}, nil
}

func (c *fakeSession) Wait() error {
	<-c.done
	return nil
}

func (c *fakeSession) Close() error {
	c.closeOnce.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
		close(c.done)
	})
	return nil
}

// fakeConnector supplies a connectFunc that returns fakeSessions and records
// connects and closes per installation.
type fakeConnector struct {
	mu       sync.Mutex
	connects map[int64]int
	closes   map[int64]int
	sessions map[int64]*fakeSession
}

func newFakeConnector() *fakeConnector {
	return &fakeConnector{connects: map[int64]int{}, closes: map[int64]int{}, sessions: map[int64]*fakeSession{}}
}

func (f *fakeConnector) connect(_ context.Context, id int64) (downstreamSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects[id]++
	session := newFakeSession(id)
	session.onClose = func() {
		f.mu.Lock()
		f.closes[id]++
		f.mu.Unlock()
	}
	f.sessions[id] = session
	return session, nil
}

func (f *fakeConnector) connectCount(id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connects[id]
}

func (f *fakeConnector) closeCount(id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes[id]
}

func (f *fakeConnector) session(id int64) *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[id]
}

func inst(id int64, account string) githubapp.Installation {
	return githubapp.Installation{ID: id, Account: account, AccountType: "Organization", RepositorySelection: "all"}
}

func TestDirectory_LookupCaseInsensitive(t *testing.T) {
	lister := &fakeLister{}
	lister.set(inst(456, "octo-org"), inst(321, "octocat"))
	dir := newDirectory(lister, nil)

	got, err := dir.lookup(context.Background(), "Octo-ORG")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != 456 {
		t.Errorf("installation ID = %d, want 456", got.ID)
	}
}

func TestDirectory_AllowList(t *testing.T) {
	lister := &fakeLister{}
	lister.set(inst(456, "octo-org"), inst(321, "octocat"))
	dir := newDirectory(lister, map[string]struct{}{"octo-org": {}})

	if _, err := dir.lookup(context.Background(), "octo-org"); err != nil {
		t.Errorf("allowed org should resolve: %v", err)
	}
	if _, err := dir.lookup(context.Background(), "octocat"); err == nil {
		t.Error("expected an error for an org not in the allow-list")
	}

	all, err := dir.all(context.Background())
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all) != 1 || all[0].Account != "octo-org" {
		t.Errorf("all() = %+v, want only octo-org", all)
	}
}

func TestDirectory_MalformedAllowListDeniesAll(t *testing.T) {
	lister := &fakeLister{}
	lister.set(inst(456, "octo-org"))
	// A malformed allow-list (e.g. GITHUB_APP_ALLOWED_ORGS=",") must fail closed.
	dir := newDirectory(lister, parseAllowedOrgs(","))

	if _, err := dir.lookup(context.Background(), "octo-org"); err == nil {
		t.Error("expected a malformed allow-list to deny all owners")
	}
	all, err := dir.all(context.Background())
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected no allowed installations, got %+v", all)
	}
}

func TestDirectory_RefreshOnMiss(t *testing.T) {
	lister := &fakeLister{}
	lister.set(inst(456, "octo-org"))
	dir := newDirectory(lister, nil)

	if _, err := dir.lookup(context.Background(), "octo-org"); err != nil {
		t.Fatalf("initial lookup: %v", err)
	}
	// A newly installed org appears only on the next listing.
	lister.set(inst(456, "octo-org"), inst(999, "new-org"))

	got, err := dir.lookup(context.Background(), "new-org")
	if err != nil {
		t.Fatalf("expected refresh-on-miss to find new-org: %v", err)
	}
	if got.ID != 999 {
		t.Errorf("installation ID = %d, want 999", got.ID)
	}
}

func TestManager_CachesAndCloses(t *testing.T) {
	fc := newFakeConnector()
	m := newManager(fc.connect)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := m.caller(ctx, 456); err != nil {
			t.Fatalf("caller: %v", err)
		}
	}
	if _, err := m.caller(ctx, 789); err != nil {
		t.Fatalf("caller: %v", err)
	}
	if got := fc.connectCount(456); got != 1 {
		t.Errorf("installation 456 connected %d times, want 1 (cached)", got)
	}
	if got := fc.connectCount(789); got != 1 {
		t.Errorf("installation 789 connected %d times, want 1", got)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fc.closeCount(456) != 1 || fc.closeCount(789) != 1 {
		t.Errorf("expected each installation closed once, got 456=%d 789=%d", fc.closeCount(456), fc.closeCount(789))
	}
	// After Close, new callers are rejected.
	if _, err := m.caller(ctx, 456); err == nil {
		t.Error("expected an error from a closed manager")
	}
}

func TestManager_ReconnectsAfterTermination(t *testing.T) {
	fc := newFakeConnector()
	m := newManager(fc.connect)
	ctx := context.Background()
	t.Cleanup(func() { _ = m.Close() })

	if _, err := m.caller(ctx, 456); err != nil {
		t.Fatalf("caller: %v", err)
	}
	if got := fc.connectCount(456); got != 1 {
		t.Fatalf("connectCount = %d, want 1", got)
	}

	// Simulate the child terminating; the eviction watcher should drop it.
	fc.session(456).Close()

	// The next call reconnects (eviction is asynchronous, so retry briefly).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := m.caller(ctx, 456); err != nil {
			t.Fatalf("caller after termination: %v", err)
		}
		if fc.connectCount(456) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected a reconnect after termination; connectCount = %d", fc.connectCount(456))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newTestRouter(lister InstallationLister, allowed map[string]struct{}, fc *fakeConnector) *Router {
	return &Router{
		dir:     newDirectory(lister, allowed),
		manager: newManager(fc.connect),
	}
}

func TestRouter_ClientForOwnerRoutes(t *testing.T) {
	lister := &fakeLister{}
	lister.set(inst(456, "octo-org"), inst(321, "octocat"))
	fc := newFakeConnector()
	r := newTestRouter(lister, nil, fc)
	t.Cleanup(func() { _ = r.Close() })

	caller, err := r.ClientForOwner(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("ClientForOwner: %v", err)
	}
	res, err := caller.CallTool(context.Background(), &mcp.CallToolParams{Name: "x"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != "installation=456" {
		t.Errorf("routed to wrong installation: %q", text)
	}

	if _, err := r.ClientForOwner(context.Background(), "unknown"); err == nil {
		t.Error("expected an error for an unknown owner")
	}
}

func TestRouter_AllClients(t *testing.T) {
	lister := &fakeLister{}
	lister.set(inst(456, "octo-org"), inst(321, "octocat"))
	fc := newFakeConnector()
	r := newTestRouter(lister, nil, fc)
	t.Cleanup(func() { _ = r.Close() })

	clients, err := r.AllClients(context.Background())
	if err != nil {
		t.Fatalf("AllClients: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("got %d clients, want 2", len(clients))
	}
	owners := map[string]bool{}
	for _, c := range clients {
		owners[c.Owner] = true
	}
	if !owners["octo-org"] || !owners["octocat"] {
		t.Errorf("AllClients owners = %v, want octo-org and octocat", owners)
	}
}
