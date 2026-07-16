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

// connectFunc starts (or connects to) the downstream server for an installation
// and returns a caller plus a closer that terminates it.
type connectFunc func(installationID int64) (ToolCaller, func() error, error)

type instance struct {
	caller ToolCaller
	close  func() error
}

// manager lazily starts and caches one downstream session per installation ID.
// It is safe for concurrent use.
type manager struct {
	connect connectFunc

	mu        sync.Mutex
	instances map[int64]*instance
	closed    bool
}

func newManager(connect connectFunc) *manager {
	return &manager{connect: connect, instances: make(map[int64]*instance)}
}

// caller returns a cached or newly-connected caller for the installation.
func (m *manager) caller(installationID int64) (ToolCaller, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, errors.New("downstream manager is closed")
	}
	if inst, ok := m.instances[installationID]; ok {
		return inst.caller, nil
	}
	caller, closeFn, err := m.connect(installationID)
	if err != nil {
		return nil, fmt.Errorf("starting github-mcp-server for installation %d: %w", installationID, err)
	}
	m.instances[installationID] = &instance{caller: caller, close: closeFn}
	return caller, nil
}

// Close shuts down all cached downstream sessions.
func (m *manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	var errs []error
	for id, inst := range m.instances {
		if err := inst.close(); err != nil {
			errs = append(errs, fmt.Errorf("closing installation %d: %w", id, err))
		}
		delete(m.instances, id)
	}
	return errors.Join(errs...)
}
