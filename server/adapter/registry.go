package adapter

import (
	"fmt"
	"sync"
)

// Registry manages agent adapters by key.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]AgentAdapter
}

// NewRegistry creates a new adapter registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]AgentAdapter)}
}

// Register adds an adapter to the registry.
func (r *Registry) Register(a AgentAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Key()] = a
}

// Get returns the adapter for a key.
func (r *Registry) Get(key string) (AgentAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[key]
	if !ok {
		return nil, fmt.Errorf("adapter not found: %q", key)
	}
	return a, nil
}

// MustGet returns the adapter for a key, panics if not found.
func (r *Registry) MustGet(key string) AgentAdapter {
	a, err := r.Get(key)
	if err != nil {
		panic(err)
	}
	return a
}

// Keys returns all registered adapter keys.
func (r *Registry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.adapters))
	for k := range r.adapters {
		keys = append(keys, k)
	}
	return keys
}