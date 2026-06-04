package task

import (
	"context"
	"sync"
)

// Manager tracks the cancel function of every in-flight task on this daemon so
// the HTTP API can cancel a running task by id (context cancel -> SIGTERM).
type Manager struct {
	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

func NewManager() *Manager {
	return &Manager{cancels: map[int64]context.CancelFunc{}}
}

// Start derives a cancelable context for a task and registers its cancel func.
// The returned done() must be called when the task finishes to deregister it.
func (m *Manager) Start(parent context.Context, id int64) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.cancels[id] = cancel
	m.mu.Unlock()

	done := func() {
		m.mu.Lock()
		delete(m.cancels, id)
		m.mu.Unlock()
		cancel() // safe to call twice; releases context resources
	}
	return ctx, done
}

// Cancel cancels a running task if it is owned by this daemon. Returns true if
// the task was found in-flight here.
func (m *Manager) Cancel(id int64) bool {
	m.mu.Lock()
	cancel, ok := m.cancels[id]
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Inflight returns the number of tasks currently running on this daemon.
func (m *Manager) Inflight() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.cancels)
}
