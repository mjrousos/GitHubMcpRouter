package downstream

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolCaller forwards a tool call to a downstream github-mcp-server. It is the
// subset of *mcp.ClientSession that the router needs.
type ToolCaller interface {
	CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

// downstreamSession is a connected child session: it forwards tool calls, can be
// awaited for termination, and can be closed. *mcp.ClientSession satisfies it.
type downstreamSession interface {
	ToolCaller
	Wait() error
	Close() error
}

// connectFunc establishes a session for an installation. The context bounds the
// connection attempt (process start + handshake).
type connectFunc func(ctx context.Context, installationID int64) (downstreamSession, error)

type instance struct {
	once    sync.Once
	session downstreamSession
	err     error
}

// manager lazily starts and caches one downstream session per installation ID.
// Connections are established without holding the manager lock, so a slow or
// hung child never blocks other installations or Close. A terminated child is
// evicted automatically so the next request reconnects. It is safe for
// concurrent use.
type manager struct {
	connect connectFunc

	mu        sync.Mutex
	instances map[int64]*instance
	closed    bool
}

func newManager(connect connectFunc) *manager {
	return &manager{connect: connect, instances: make(map[int64]*instance)}
}

var errManagerClosed = errors.New("downstream manager is closed")

// caller returns a cached or newly-connected caller for the installation. The
// context bounds a first-time connection but does not affect a cached session.
func (m *manager) caller(ctx context.Context, installationID int64) (ToolCaller, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errManagerClosed
	}
	inst, ok := m.instances[installationID]
	if !ok {
		inst = &instance{}
		m.instances[installationID] = inst
	}
	m.mu.Unlock()

	// Connect at most once per instance, without holding the manager lock.
	// Concurrent callers for the same installation share this single attempt;
	// callers for other installations proceed in parallel.
	inst.once.Do(func() {
		session, err := m.connect(ctx, installationID)
		if err != nil {
			inst.err = err
			m.forget(installationID, inst) // allow a later retry
			return
		}

		m.mu.Lock()
		if m.closed {
			// The manager was closed while we connected; discard the session.
			m.mu.Unlock()
			_ = session.Close()
			inst.err = errManagerClosed
			return
		}
		inst.session = session
		m.mu.Unlock()

		// Evict the instance when the child terminates so the next call
		// reconnects instead of reusing a dead session.
		go func() {
			_ = session.Wait()
			m.forget(installationID, inst)
		}()
	})

	if inst.err != nil {
		return nil, fmt.Errorf("starting github-mcp-server for installation %d: %w", installationID, inst.err)
	}
	return inst.session, nil
}

// forget removes inst from the cache if it is still the current entry.
func (m *manager) forget(installationID int64, inst *instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.instances[installationID] == inst {
		delete(m.instances, installationID)
	}
}

// Close shuts down all cached downstream sessions. In-flight connections are
// unblocked by cancelling the router's context (see Router.Close).
func (m *manager) Close() error {
	m.mu.Lock()
	m.closed = true
	var sessions []downstreamSession
	for _, inst := range m.instances {
		if inst.session != nil {
			sessions = append(sessions, inst.session)
		}
	}
	m.instances = make(map[int64]*instance)
	m.mu.Unlock()

	var errs []error
	for _, session := range sessions {
		if err := session.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
