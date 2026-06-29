package workflow

import "fmt"

var globalRegistry = NewNodeRegistry()

func NewNodeRegistry() *NodeRegistry {
	return &NodeRegistry{factories: map[NodeKind]NodeFactory{}}
}

func GetRegistry() *NodeRegistry {
	return globalRegistry
}

func (r *NodeRegistry) Register(kind NodeKind, factory NodeFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[kind] = factory
}

func (r *NodeRegistry) Resolve(kind NodeKind) (WorkflowNode, error) {
	r.mu.RLock()
	factory := r.factories[kind]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("workflow node kind %q is not registered", kind)
	}
	return factory(), nil
}
