package codegraph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoGraphAddNode(t *testing.T) {
	g := NewRepoGraph()

	node := &RepoNode{
		RepoID:      "repo1",
		Name:        "test-repo",
		Path:        "/tmp/test",
		Initialized: true,
		Role:        "backend",
		CreatedAt:   1234567890,
	}

	err := g.AddNode(node)
	if err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}

	// Adding same node should fail
	err = g.AddNode(node)
	if err == nil {
		t.Fatal("expected error for duplicate node, got nil")
	}
}

func TestRepoGraphRemoveNode(t *testing.T) {
	g := NewRepoGraph()

	node := &RepoNode{
		RepoID:      "repo1",
		Name:        "test-repo",
		Path:        "/tmp/test",
		Initialized: true,
		Role:        "backend",
	}

	g.AddNode(node)

	// Add an edge to test edge cleanup
	edge := &RepoEdge{
		ID:           "edge1",
		SourceRepoID: "repo1",
		TargetRepoID: "repo2",
		EdgeType:     EdgeHTTP,
		Endpoint:     "GET /api/test",
	}
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})
	g.AddEdge(edge)

	err := g.RemoveNode("repo1")
	if err != nil {
		t.Fatalf("RemoveNode failed: %v", err)
	}

	// Verify node is gone
	_, err = g.GetNode("repo1")
	if err == nil {
		t.Error("expected error for removed node, got nil")
	}

	// Verify edge is gone
	edges := g.ListEdges()
	for _, e := range edges {
		if e.ID == "edge1" {
			t.Error("edge should have been removed with node")
		}
	}
}

func TestRepoGraphGetNode(t *testing.T) {
	g := NewRepoGraph()

	node := &RepoNode{
		RepoID:      "repo1",
		Name:        "test-repo",
		Path:        "/tmp/test",
		Initialized: true,
		Role:        "backend",
	}

	g.AddNode(node)

	retrieved, err := g.GetNode("repo1")
	if err != nil {
		t.Fatalf("GetNode failed: %v", err)
	}
	if retrieved.RepoID != node.RepoID {
		t.Errorf("expected RepoID %s, got %s", node.RepoID, retrieved.RepoID)
	}

	_, err = g.GetNode("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent node, got nil")
	}
}

func TestRepoGraphListNodes(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})

	nodes := g.ListNodes()
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestRepoGraphUpdateNode(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{
		RepoID:      "repo1",
		Name:        "test-repo",
		Path:        "/tmp/test",
		Initialized: false,
		Role:        "shared",
	})

	err := g.UpdateNode("repo1", func(node *RepoNode) error {
		node.Initialized = true
		node.Role = "backend"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateNode failed: %v", err)
	}

	node, _ := g.GetNode("repo1")
	if !node.Initialized {
		t.Error("expected Initialized to be true")
	}
	if node.Role != "backend" {
		t.Errorf("expected Role 'backend', got '%s'", node.Role)
	}
}

func TestRepoGraphAddEdge(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})

	edge := &RepoEdge{
		ID:           "edge1",
		SourceRepoID: "repo1",
		TargetRepoID: "repo2",
		EdgeType:     EdgeHTTP,
		Endpoint:     "GET /api/test",
		Confidence:   0.95,
	}

	err := g.AddEdge(edge)
	if err != nil {
		t.Fatalf("AddEdge failed: %v", err)
	}

	// Adding same edge should fail
	err = g.AddEdge(edge)
	if err == nil {
		t.Fatal("expected error for duplicate edge, got nil")
	}
}

func TestRepoGraphRemoveEdge(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})

	edge := &RepoEdge{
		ID:           "edge1",
		SourceRepoID: "repo1",
		TargetRepoID: "repo2",
		EdgeType:     EdgeHTTP,
		Endpoint:     "GET /api/test",
	}

	g.AddEdge(edge)

	err := g.RemoveEdge("edge1")
	if err != nil {
		t.Fatalf("RemoveEdge failed: %v", err)
	}

	edges := g.ListEdges()
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

func TestRepoGraphGetEdge(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})

	edge := &RepoEdge{
		ID:           "edge1",
		SourceRepoID: "repo1",
		TargetRepoID: "repo2",
		EdgeType:     EdgeHTTP,
		Endpoint:     "GET /api/test",
	}

	g.AddEdge(edge)

	retrieved, err := g.GetEdge("edge1")
	if err != nil {
		t.Fatalf("GetEdge failed: %v", err)
	}
	if retrieved.ID != edge.ID {
		t.Errorf("expected ID %s, got %s", edge.ID, retrieved.ID)
	}

	_, err = g.GetEdge("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent edge, got nil")
	}
}

func TestRepoGraphListEdges(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})
	g.AddNode(&RepoNode{RepoID: "repo3", Name: "repo3", Path: "/tmp/test3"})

	g.AddEdge(&RepoEdge{ID: "edge1", SourceRepoID: "repo1", TargetRepoID: "repo2", EdgeType: EdgeHTTP, Endpoint: "GET /api/a"})
	g.AddEdge(&RepoEdge{ID: "edge2", SourceRepoID: "repo2", TargetRepoID: "repo3", EdgeType: EdgegRPC, Endpoint: "/api.B"})

	edges := g.ListEdges()
	if len(edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(edges))
	}
}

func TestRepoGraphOutgoingEdges(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})
	g.AddNode(&RepoNode{RepoID: "repo3", Name: "repo3", Path: "/tmp/test3"})

	g.AddEdge(&RepoEdge{ID: "edge1", SourceRepoID: "repo1", TargetRepoID: "repo2", EdgeType: EdgeHTTP, Endpoint: "GET /api/a"})
	g.AddEdge(&RepoEdge{ID: "edge2", SourceRepoID: "repo1", TargetRepoID: "repo3", EdgeType: EdgeDB, Endpoint: "db://test"})

	outgoing := g.GetOutgoingEdges("repo1")
	if len(outgoing) != 2 {
		t.Errorf("expected 2 outgoing edges, got %d", len(outgoing))
	}

	outgoing = g.GetOutgoingEdges("repo2")
	if len(outgoing) != 0 {
		t.Errorf("expected 0 outgoing edges for repo2, got %d", len(outgoing))
	}
}

func TestRepoGraphIncomingEdges(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})
	g.AddNode(&RepoNode{RepoID: "repo3", Name: "repo3", Path: "/tmp/test3"})

	g.AddEdge(&RepoEdge{ID: "edge1", SourceRepoID: "repo1", TargetRepoID: "repo3", EdgeType: EdgeHTTP, Endpoint: "GET /api/a"})
	g.AddEdge(&RepoEdge{ID: "edge2", SourceRepoID: "repo2", TargetRepoID: "repo3", EdgeType: EdgeEvent, Endpoint: "event/topic"})

	incoming := g.GetIncomingEdges("repo3")
	if len(incoming) != 2 {
		t.Errorf("expected 2 incoming edges, got %d", len(incoming))
	}

	incoming = g.GetIncomingEdges("repo1")
	if len(incoming) != 0 {
		t.Errorf("expected 0 incoming edges for repo1, got %d", len(incoming))
	}
}

func TestRepoGraphGetReachable(t *testing.T) {
	g := NewRepoGraph()

	// Create a dependency chain: repo1 -> repo2 -> repo3 -> repo4
	// repo5 is disconnected
	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})
	g.AddNode(&RepoNode{RepoID: "repo3", Name: "repo3", Path: "/tmp/test3"})
	g.AddNode(&RepoNode{RepoID: "repo4", Name: "repo4", Path: "/tmp/test4"})
	g.AddNode(&RepoNode{RepoID: "repo5", Name: "repo5", Path: "/tmp/test5"})

	g.AddEdge(&RepoEdge{ID: "e1", SourceRepoID: "repo1", TargetRepoID: "repo2", EdgeType: EdgeHTTP, Endpoint: "GET /api/1"})
	g.AddEdge(&RepoEdge{ID: "e2", SourceRepoID: "repo2", TargetRepoID: "repo3", EdgeType: EdgeHTTP, Endpoint: "GET /api/2"})
	g.AddEdge(&RepoEdge{ID: "e3", SourceRepoID: "repo3", TargetRepoID: "repo4", EdgeType: EdgeHTTP, Endpoint: "GET /api/3"})

	// Test upstream (dependencies) from repo4
	upstream := g.GetReachable([]string{"repo4"}, "upstream", 10)
	if upstream["repo1"] != true {
		t.Error("expected repo4 to reach repo1 upstream")
	}
	if upstream["repo4"] != true {
		t.Error("expected repo4 to reach itself")
	}

	// Test downstream (dependents) from repo1
	downstream := g.GetReachable([]string{"repo1"}, "downstream", 10)
	if downstream["repo4"] != true {
		t.Error("expected repo1 to reach repo4 downstream")
	}

	// Test depth limit
	depth2 := g.GetReachable([]string{"repo1"}, "downstream", 2)
	if depth2["repo4"] == true {
		t.Error("expected repo4 NOT to be reachable within 2 hops downstream")
	}
}

func TestRepoGraphMarshalUnmarshal(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{
		RepoID:      "repo1",
		Name:        "test-repo",
		Path:        "/tmp/test",
		Initialized: true,
		Role:        "backend",
		CreatedAt:   1234567890,
	})

	g.AddNode(&RepoNode{
		RepoID:      "repo2",
		Name:        "test-repo2",
		Path:        "/tmp/test2",
		Initialized: false,
		Role:        "frontend",
		CreatedAt:   9876543210,
	})

	g.AddEdge(&RepoEdge{
		ID:           "edge1",
		SourceRepoID: "repo1",
		TargetRepoID: "repo2",
		EdgeType:     EdgeHTTP,
		Endpoint:     "GET /api/test",
		Confidence:   0.95,
		DiscoveredAt: 1111111111,
	})

	data, err := g.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	g2 := NewRepoGraph()
	err = g2.UnmarshalJSON(data)
	if err != nil {
		t.Fatalf("UnmarshalJSON failed: %v", err)
	}

	nodes := g2.ListNodes()
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes after unmarshal, got %d", len(nodes))
	}

	edges := g2.ListEdges()
	if len(edges) != 1 {
		t.Errorf("expected 1 edge after unmarshal, got %d", len(edges))
	}
}

// Compatibility shim tests

func TestRepoGraphCompatibilityShims(t *testing.T) {
	// Create temp dir with .codegraph to simulate initialized repo
	tmpDir := t.TempDir()
	codegraphDir := filepath.Join(tmpDir, ".codegraph")
	if err := os.MkdirAll(codegraphDir, 0755); err != nil {
		t.Fatalf("failed to create .codegraph dir: %v", err)
	}
	dbPath := filepath.Join(codegraphDir, "codegraph.db")
	if err := os.WriteFile(dbPath, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create db file: %v", err)
	}

	g := NewRepoGraph()

	// Test RegisterRepo
	err := g.RegisterRepo("repo1", tmpDir, "test-repo", "backend")
	if err != nil {
		t.Fatalf("RegisterRepo failed: %v", err)
	}

	// Test ResolveRepoPath
	path, err := g.ResolveRepoPath("repo1")
	if err != nil {
		t.Fatalf("ResolveRepoPath failed: %v", err)
	}
	if path != tmpDir {
		t.Errorf("expected path %s, got %s", tmpDir, path)
	}

	// Test GetRepo
	repo, err := g.GetRepo("repo1")
	if err != nil {
		t.Fatalf("GetRepo failed: %v", err)
	}
	if repo.RepoID != "repo1" {
		t.Errorf("expected RepoID 'repo1', got '%s'", repo.RepoID)
	}
	if repo.Name != "test-repo" {
		t.Errorf("expected Name 'test-repo', got '%s'", repo.Name)
	}

	// Test ListRepos
	repos := g.ListRepos()
	if len(repos) != 1 {
		t.Errorf("expected 1 repo, got %d", len(repos))
	}

	// Test UnregisterRepo
	err = g.UnregisterRepo("repo1")
	if err != nil {
		t.Fatalf("UnregisterRepo failed: %v", err)
	}

	_, err = g.GetRepo("repo1")
	if err == nil {
		t.Error("expected error for unregistered repo, got nil")
	}
}

func TestRepoGraphEdgeWithNonexistentNodes(t *testing.T) {
	g := NewRepoGraph()

	g.AddNode(&RepoNode{RepoID: "repo1", Name: "repo1", Path: "/tmp/test1"})

	// Try to add edge with nonexistent target
	err := g.AddEdge(&RepoEdge{
		ID:           "edge1",
		SourceRepoID: "repo1",
		TargetRepoID: "nonexistent",
		EdgeType:     EdgeHTTP,
		Endpoint:     "GET /api/test",
	})
	if err == nil {
		t.Error("expected error for nonexistent target node, got nil")
	}

	// Add the missing node and retry
	g.AddNode(&RepoNode{RepoID: "repo2", Name: "repo2", Path: "/tmp/test2"})
	err = g.AddEdge(&RepoEdge{
		ID:           "edge1",
		SourceRepoID: "repo1",
		TargetRepoID: "repo2",
		EdgeType:     EdgeHTTP,
		Endpoint:     "GET /api/test",
	})
	if err != nil {
		t.Fatalf("AddEdge failed after adding missing node: %v", err)
	}
}
