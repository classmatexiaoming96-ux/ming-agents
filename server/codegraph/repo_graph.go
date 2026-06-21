package codegraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RepoNode represents a repository as a node in the graph
type RepoNode struct {
	RepoID      string `json:"repoID"`
	Name        string `json:"name"`
	Path        string `json:"path"`         // absolute path
	Language    string `json:"language"`     // e.g. "go", "typescript", "python"
	Initialized bool   `json:"initialized"`  // has .codegraph/codegraph.db
	Role        string `json:"role"`         // "frontend" | "backend" | "shared" | "tool"
	CreatedAt   int64  `json:"createdAt"`
}

// RepoEdgeType represents the type of relationship between repos
type RepoEdgeType string

const (
	EdgeHTTP  RepoEdgeType = "http"
	EdgegRPC  RepoEdgeType = "grpc"
	EdgeEvent RepoEdgeType = "event"
	EdgeDB    RepoEdgeType = "db"
)

// RepoEdge represents a call/dependency relationship between two repos
type RepoEdge struct {
	ID          string      `json:"id"`
	SourceRepoID string     `json:"sourceRepoID"` // caller
	TargetRepoID string     `json:"targetRepoID"` // callee
	EdgeType    RepoEdgeType `json:"edgeType"`     // http | grpc | event | db
	Endpoint    string      `json:"endpoint"`     // e.g. "POST /api/tasks" or "/api/tasks.GetTasks"
	Description string      `json:"description"`
	Confidence  float64     `json:"confidence"`   // 0.0-1.0, how confident we are in this edge
	DiscoveredAt int64      `json:"discoveredAt"`
}

// RepoGraph is the main graph structure
type RepoGraph struct {
	mu    sync.RWMutex
	nodes map[string]*RepoNode // repoID → RepoNode
	edges []*RepoEdge          // all edges
	// for fast lookup by source/target
	edgesBySource map[string][]*RepoEdge // sourceRepoID → edges
	edgesByTarget map[string][]*RepoEdge // targetRepoID → edges
}

// NewRepoGraph creates a new RepoGraph
func NewRepoGraph() *RepoGraph {
	return &RepoGraph{
		nodes:         make(map[string]*RepoNode),
		edges:         make([]*RepoEdge, 0),
		edgesBySource: make(map[string][]*RepoEdge),
		edgesByTarget: make(map[string][]*RepoEdge),
	}
}

// --- Node operations ---

// AddNode adds a new node to the graph. Returns error if node already exists.
func (g *RepoGraph) AddNode(node *RepoNode) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.nodes[node.RepoID]; exists {
		return fmt.Errorf("node already exists: %s", node.RepoID)
	}
	g.nodes[node.RepoID] = node
	return nil
}

// RemoveNode removes a node and all its edges (both incoming and outgoing)
func (g *RepoGraph) RemoveNode(repoID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.nodes[repoID]; !exists {
		return fmt.Errorf("unknown node: %s", repoID)
	}

	// Remove from nodes map
	delete(g.nodes, repoID)

	// Remove outgoing edges
	if edges, exists := g.edgesBySource[repoID]; exists {
		for _, edge := range edges {
			// Remove edge from edges slice
			for i, e := range g.edges {
				if e.ID == edge.ID {
					g.edges = append(g.edges[:i], g.edges[i+1:]...)
					break
				}
			}
		}
		delete(g.edgesBySource, repoID)
	}

	// Remove incoming edges
	if edges, exists := g.edgesByTarget[repoID]; exists {
		for _, edge := range edges {
			// Remove edge from edges slice
			for i, e := range g.edges {
				if e.ID == edge.ID {
					g.edges = append(g.edges[:i], g.edges[i+1:]...)
					break
				}
			}
		}
		delete(g.edgesByTarget, repoID)
	}

	return nil
}

// GetNode returns a node by ID
func (g *RepoGraph) GetNode(repoID string) (*RepoNode, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	node, exists := g.nodes[repoID]
	if !exists {
		return nil, fmt.Errorf("unknown node: %s", repoID)
	}
	return node, nil
}

// ListNodes returns all nodes
func (g *RepoGraph) ListNodes() []*RepoNode {
	g.mu.RLock()
	defer g.mu.RUnlock()

	nodes := make([]*RepoNode, 0, len(g.nodes))
	for _, node := range g.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// UpdateNode performs an atomic update on a node
func (g *RepoGraph) UpdateNode(repoID string, fn func(*RepoNode) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	node, exists := g.nodes[repoID]
	if !exists {
		return fmt.Errorf("unknown node: %s", repoID)
	}

	if err := fn(node); err != nil {
		return err
	}

	g.nodes[repoID] = node
	return nil
}

// --- Edge operations ---

// AddEdge adds a new edge to the graph. Returns error if edge ID already exists.
func (g *RepoGraph) AddEdge(edge *RepoEdge) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Check if edge ID already exists
	for _, e := range g.edges {
		if e.ID == edge.ID {
			return fmt.Errorf("edge already exists: %s", edge.ID)
		}
	}

	// Verify source and target nodes exist
	if _, exists := g.nodes[edge.SourceRepoID]; !exists {
		return fmt.Errorf("unknown source node: %s", edge.SourceRepoID)
	}
	if _, exists := g.nodes[edge.TargetRepoID]; !exists {
		return fmt.Errorf("unknown target node: %s", edge.TargetRepoID)
	}

	g.edges = append(g.edges, edge)
	g.edgesBySource[edge.SourceRepoID] = append(g.edgesBySource[edge.SourceRepoID], edge)
	g.edgesByTarget[edge.TargetRepoID] = append(g.edgesByTarget[edge.TargetRepoID], edge)

	return nil
}

// RemoveEdge removes an edge by ID
func (g *RepoGraph) RemoveEdge(edgeID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	var found bool
	var edge *RepoEdge

	// Find the edge
	for _, e := range g.edges {
		if e.ID == edgeID {
			found = true
			edge = e
			break
		}
	}

	if !found {
		return fmt.Errorf("unknown edge: %s", edgeID)
	}

	// Remove from edges slice
	for i, e := range g.edges {
		if e.ID == edgeID {
			g.edges = append(g.edges[:i], g.edges[i+1:]...)
			break
		}
	}

	// Remove from edgesBySource
	if edges, exists := g.edgesBySource[edge.SourceRepoID]; exists {
		for i, e := range edges {
			if e.ID == edgeID {
				g.edgesBySource[edge.SourceRepoID] = append(edges[:i], edges[i+1:]...)
				break
			}
		}
	}

	// Remove from edgesByTarget
	if edges, exists := g.edgesByTarget[edge.TargetRepoID]; exists {
		for i, e := range edges {
			if e.ID == edgeID {
				g.edgesByTarget[edge.TargetRepoID] = append(edges[:i], edges[i+1:]...)
				break
			}
		}
	}

	return nil
}

// GetEdge returns an edge by ID
func (g *RepoGraph) GetEdge(edgeID string) (*RepoEdge, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, e := range g.edges {
		if e.ID == edgeID {
			return e, nil
		}
	}
	return nil, fmt.Errorf("unknown edge: %s", edgeID)
}

// ListEdges returns all edges
func (g *RepoGraph) ListEdges() []*RepoEdge {
	g.mu.RLock()
	defer g.mu.RUnlock()

	edges := make([]*RepoEdge, len(g.edges))
	copy(edges, g.edges)
	return edges
}

// --- Graph traversal ---

// GetOutgoingEdges returns edges where this repo is the source (dependencies)
func (g *RepoGraph) GetOutgoingEdges(repoID string) []*RepoEdge {
	g.mu.RLock()
	defer g.mu.RUnlock()

	edges, exists := g.edgesBySource[repoID]
	if !exists {
		return []*RepoEdge{}
	}

	result := make([]*RepoEdge, len(edges))
	copy(result, edges)
	return result
}

// GetIncomingEdges returns edges where this repo is the target (dependents)
func (g *RepoGraph) GetIncomingEdges(repoID string) []*RepoEdge {
	g.mu.RLock()
	defer g.mu.RUnlock()

	edges, exists := g.edgesByTarget[repoID]
	if !exists {
		return []*RepoEdge{}
	}

	result := make([]*RepoEdge, len(edges))
	copy(result, edges)
	return result
}

// GetReachable returns all repoIDs reachable within maxDepth hops
// direction: "upstream" (dependencies) or "downstream" (dependents)
func (g *RepoGraph) GetReachable(repos []string, direction string, maxDepth int) map[string]bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	reachable := make(map[string]bool)
	visited := make(map[string]bool)
	queue := make([]string, 0)

	// Initialize queue with starting repos
	for _, repoID := range repos {
		if !visited[repoID] {
			visited[repoID] = true
			queue = append(queue, repoID)
			reachable[repoID] = true
		}
	}

	// BFS traversal
	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		currentLevelSize := len(queue)
		for i := 0; i < currentLevelSize; i++ {
			current := queue[0]
			queue = queue[1:]

			var neighbors []string
			if direction == "upstream" {
				// Upstream: follow edges from target to source (what this repo depends on)
				neighbors = g.getUpstreamNeighbors(current)
			} else {
				// Downstream: follow edges from source to target (what depends on this repo)
				neighbors = g.getDownstreamNeighbors(current)
			}

			for _, neighbor := range neighbors {
				if !visited[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
					reachable[neighbor] = true
				}
			}
		}
	}

	return reachable
}

func (g *RepoGraph) getUpstreamNeighbors(repoID string) []string {
	var neighbors []string
	if edges, exists := g.edgesByTarget[repoID]; exists {
		for _, edge := range edges {
			neighbors = append(neighbors, edge.SourceRepoID)
		}
	}
	return neighbors
}

func (g *RepoGraph) getDownstreamNeighbors(repoID string) []string {
	var neighbors []string
	if edges, exists := g.edgesBySource[repoID]; exists {
		for _, edge := range edges {
			neighbors = append(neighbors, edge.TargetRepoID)
		}
	}
	return neighbors
}

// --- Serialization ---

// MarshalJSON serializes the graph to JSON
func (g *RepoGraph) MarshalJSON() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	type graphJSON struct {
		Nodes []*RepoNode `json:"nodes"`
		Edges []*RepoEdge `json:"edges"`
	}

	return json.Marshal(graphJSON{
		Nodes: g.ListNodes(),
		Edges: g.ListEdges(),
	})
}

// UnmarshalJSON deserializes the graph from JSON
func (g *RepoGraph) UnmarshalJSON(data []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	type graphJSON struct {
		Nodes []*RepoNode `json:"nodes"`
		Edges []*RepoEdge `json:"edges"`
	}

	var gj graphJSON
	if err := json.Unmarshal(data, &gj); err != nil {
		return err
	}

	// Reset the graph
	g.nodes = make(map[string]*RepoNode)
	g.edges = make([]*RepoEdge, 0)
	g.edgesBySource = make(map[string][]*RepoEdge)
	g.edgesByTarget = make(map[string][]*RepoEdge)

	// Load nodes
	for _, node := range gj.Nodes {
		g.nodes[node.RepoID] = node
	}

	// Load edges
	for _, edge := range gj.Edges {
		g.edges = append(g.edges, edge)
		g.edgesBySource[edge.SourceRepoID] = append(g.edgesBySource[edge.SourceRepoID], edge)
		g.edgesByTarget[edge.TargetRepoID] = append(g.edgesByTarget[edge.TargetRepoID], edge)
	}

	return nil
}

// --- Compatibility shims for RepoRegistry interface ---

// IsInitialized checks if a path has an initialized .codegraph/ database.
func IsInitialized(path string) bool {
	dbPath := filepath.Join(path, ".codegraph", "codegraph.db")
	_, err := os.Stat(dbPath)
	return err == nil
}

// RepoConfig holds configuration for a registered repository (legacy compatibility)
type RepoConfig struct {
	RepoID  string `json:"repoID"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Label   string `json:"label"`
}

// GetRepo returns the config for a specific repo (compatibility shim)
func (g *RepoGraph) GetRepo(repoID string) (*RepoConfig, error) {
	node, err := g.GetNode(repoID)
	if err != nil {
		return nil, err
	}
	return &RepoConfig{
		RepoID: node.RepoID,
		Path:   node.Path,
		Name:   node.Name,
		Label:  node.Role,
	}, nil
}

// ListRepos returns all registered repositories (compatibility shim)
func (g *RepoGraph) ListRepos() []RepoConfig {
	nodes := g.ListNodes()
	repos := make([]RepoConfig, 0, len(nodes))
	for _, node := range nodes {
		repos = append(repos, RepoConfig{
			RepoID: node.RepoID,
			Path:   node.Path,
			Name:   node.Name,
			Label:  node.Role,
		})
	}
	return repos
}

// RegisterRepo adds a repository to the registry (compatibility shim)
func (g *RepoGraph) RegisterRepo(repoID, path, name, label string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}

	if !IsInitialized(absPath) {
		return fmt.Errorf("repository not initialized: %s", absPath)
	}

	node := &RepoNode{
		RepoID:      repoID,
		Name:        name,
		Path:        absPath,
		Initialized: true,
		Role:        label,
		CreatedAt:   0,
	}

	return g.AddNode(node)
}

// UnregisterRepo removes a repository from the registry (compatibility shim)
func (g *RepoGraph) UnregisterRepo(repoID string) error {
	return g.RemoveNode(repoID)
}

// ResolveRepoPath returns the absolute path for a repo ID (compatibility shim)
func (g *RepoGraph) ResolveRepoPath(repoID string) (string, error) {
	node, err := g.GetNode(repoID)
	if err != nil {
		return "", err
	}
	return node.Path, nil
}

// ErrNodeNotFound is returned when a node is not found
var ErrNodeNotFound = errors.New("node not found")
