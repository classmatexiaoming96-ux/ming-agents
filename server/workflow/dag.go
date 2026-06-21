package workflow

import (
	"fmt"
)

// DAGError represents a DAG error.
type DAGError struct {
	Msg string
}

func (e *DAGError) Error() string { return e.Msg }

// DAGError types.
func (e *DAGError) IsCyclic() bool { return e.Msg == "cyclic" }

// Node represents a node in the DAG.
type Node struct {
	ID       string
	Name     string
	Type     string
	When     *string        // conditional expression for this node
	Inputs   map[string]string // local name → upstream node.output key
	Outputs  []string
	metadata any
}

// DAG builds and analyzes the step dependency graph.
type DAG struct {
	nodes    map[string]*Node
	edges    map[string][]string // from → []to
	inDegree map[string]int
}

// NewDAG creates a new DAG.
func NewDAG() *DAG {
	return &DAG{
		nodes:    make(map[string]*Node),
		edges:    make(map[string][]string),
		inDegree: make(map[string]int),
	}
}

// AddNode adds a node to the DAG.
func (d *DAG) AddNode(n *Node) {
	if _, exists := d.nodes[n.ID]; exists {
		return
	}
	d.nodes[n.ID] = n
	d.inDegree[n.ID] = 0
}

// AddEdge adds a directed edge from → to.
func (d *DAG) AddEdge(from, to string) error {
	_, ok := d.nodes[from]
	if !ok {
		return fmt.Errorf("AddEdge: from node %q not found", from)
	}
	_, ok = d.nodes[to]
	if !ok {
		return fmt.Errorf("AddEdge: to node %q not found", to)
	}
	// Avoid duplicate edges.
	for _, e := range d.edges[from] {
		if e == to {
			return nil
		}
	}
	d.edges[from] = append(d.edges[from], to)
	d.inDegree[to]++
	return nil
}

// TopologicalSort returns nodes in topological order using Kahn's algorithm.
// Returns an error if a cycle is detected.
func (d *DAG) TopologicalSort() ([]*Node, error) {
	// Copy in-degree so we don't mutate the original.
	deg := make(map[string]int, len(d.inDegree))
	for k, v := range d.inDegree {
		deg[k] = v
	}

	// Queue of nodes with in-degree 0.
	var q []string
	for id, d := range deg {
		if d == 0 {
			q = append(q, id)
		}
	}
	// Process in order.
	var order []*Node
	for len(q) > 0 {
		// Pop front.
		id := q[0]
		q = q[1:]
		n := d.nodes[id]
		order = append(order, n)
		for _, to := range d.edges[id] {
			deg[to]--
			if deg[to] == 0 {
				q = append(q, to)
			}
		}
	}
	if len(order) != len(d.nodes) {
		return nil, fmt.Errorf("cyclic dependency graph")
	}
	return order, nil
}

// DetectCycle checks for cycles using DFS coloring.
func (d *DAG) DetectCycle() bool {
	const white, gray, black = 0, 1, 2
	color := make(map[string]int)
	for id := range d.nodes {
		color[id] = white
	}
	var dfs func(string) bool
	dfs = func(id string) bool {
		color[id] = gray
		for _, to := range d.edges[id] {
			if color[to] == gray {
				return true // back edge = cycle
			}
			if color[to] == white && dfs(to) {
				return true
			}
		}
		color[id] = black
		return false
	}
	for id := range d.nodes {
		if color[id] == white && dfs(id) {
			return true
		}
	}
	return false
}

// BuildFromWDL constructs the DAG from a parsed WDL.
func (d *DAG) BuildFromWDL(steps []*Step) error {
	// First pass: create all nodes.
	for _, s := range steps {
		d.AddNode(&Node{
			ID:      s.Name,
			Name:    s.Name,
			Type:    s.StepType,
			Inputs:  make(map[string]string),
			Outputs: s.Outputs,
		})
	}
	// Second pass: add edges based on input references.
	for _, s := range steps {
		if s.Inputs == nil {
			continue
		}
		for _, v := range s.Inputs {
			ref := extractRef(v)
			if ref == "" {
				continue
			}
			parts := splitRef(ref)
			if parts == nil {
				continue
			}
			// Edge: steps[parts[0]] → steps[s.Name]
			if err := d.AddEdge(parts[0], s.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// extractRef extracts the reference from "${...}" or returns the raw string.
func extractRef(v any) string {
	if s, ok := v.(string); ok {
		if len(s) >= 4 && s[:2] == "${" && s[len(s)-1] == '}' {
			return s[2 : len(s)-1]
		}
	}
	return ""
}

func splitRef(ref string) []string {
	for i := 0; i < len(ref); i++ {
		if ref[i] == '.' {
			return []string{ref[:i], ref[i+1:]}
		}
	}
	return nil
}

// Nodes returns all nodes in the DAG.
func (d *DAG) Nodes() []*Node {
	nodes := make([]*Node, 0, len(d.nodes))
	for _, n := range d.nodes {
		nodes = append(nodes, n)
	}
	return nodes
}

// Node returns a node by ID.
func (d *DAG) Node(id string) *Node {
	return d.nodes[id]
}

// Parents returns the IDs of nodes that have edges to the given node.
func (d *DAG) Parents(id string) []string {
	var parents []string
	for from, tos := range d.edges {
		for _, to := range tos {
			if to == id {
				parents = append(parents, from)
				break
			}
		}
	}
	return parents
}

// Children returns the IDs of nodes that the given node has edges to.
func (d *DAG) Children(id string) []string {
	return d.edges[id]
}

// InDegree returns the in-degree of a node.
func (d *DAG) InDegree(id string) int {
	return d.inDegree[id]
}

// UpdateInDegree decrements the in-degree of a node.
// Used at runtime when a parent step completes to unblock dependents.
func (d *DAG) UpdateInDegree(id string) {
	if d.inDegree[id] > 0 {
		d.inDegree[id]--
	}
}

// ResetInDegree resets the in-degree of all nodes from the current edge state.
// Used when recovering or reinitializing the scheduler.
func (d *DAG) ResetInDegree() {
	for id := range d.nodes {
		d.inDegree[id] = 0
	}
	for from, tos := range d.edges {
		_ = from
		for _, to := range tos {
			d.inDegree[to]++
		}
	}
}