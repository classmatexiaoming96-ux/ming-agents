package codegraph

import (
	"encoding/json"
	"testing"
)

func TestStatusResultParsing(t *testing.T) {
	raw := `{
		"initialized": true,
		"projectPath": "/test/project",
		"fileCount": 42,
		"nodeCount": 1337,
		"edgeCount": 9001,
		"dbSizeBytes": 1048576,
		"backend": "sqlite",
		"journalMode": "wal",
		"nodesByKind": {"function": 10, "type": 5},
		"languages": ["go", "python"],
		"pendingChanges": {"added": 1, "modified": 2, "removed": 0}
	}`

	var result StatusResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("failed to unmarshal StatusResult: %v", err)
	}

	if !result.Initialized {
		t.Error("expected Initialized to be true")
	}
	if result.ProjectPath != "/test/project" {
		t.Errorf("expected ProjectPath '/test/project', got '%s'", result.ProjectPath)
	}
	if result.FileCount != 42 {
		t.Errorf("expected FileCount 42, got %d", result.FileCount)
	}
	if result.NodeCount != 1337 {
		t.Errorf("expected NodeCount 1337, got %d", result.NodeCount)
	}
	if result.EdgeCount != 9001 {
		t.Errorf("expected EdgeCount 9001, got %d", result.EdgeCount)
	}
	if result.DbsizeBytes != 1048576 {
		t.Errorf("expected DbsizeBytes 1048576, got %d", result.DbsizeBytes)
	}
	if result.Backend != "sqlite" {
		t.Errorf("expected Backend 'sqlite', got '%s'", result.Backend)
	}
	if len(result.Languages) != 2 {
		t.Errorf("expected 2 languages, got %d", len(result.Languages))
	}
	if result.PendingChanges == nil {
		t.Fatal("expected PendingChanges to be non-nil")
	}
	if result.PendingChanges.Added != 1 {
		t.Errorf("expected PendingChanges.Added 1, got %d", result.PendingChanges.Added)
	}
}

func TestSearchResultParsing(t *testing.T) {
	raw := `{
		"node": {
			"id": "node123",
			"kind": "function",
			"name": "TestFunc",
			"qualifiedName": "pkg.TestFunc",
			"filePath": "/test/main.go",
			"language": "go",
			"startLine": 10,
			"endLine": 20,
			"startColumn": 1,
			"endColumn": 40,
			"signature": "func TestFunc() string",
			"isExported": true
		},
		"score": 0.95,
		"highlights": ["TestFunc", "function"]
	}`

	var result SearchResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("failed to unmarshal SearchResult: %v", err)
	}

	if result.Score != 0.95 {
		t.Errorf("expected Score 0.95, got %f", result.Score)
	}
	if result.Node.ID != "node123" {
		t.Errorf("expected Node.ID 'node123', got '%s'", result.Node.ID)
	}
	if result.Node.Kind != "function" {
		t.Errorf("expected Node.Kind 'function', got '%s'", result.Node.Kind)
	}
	if result.Node.Name != "TestFunc" {
		t.Errorf("expected Node.Name 'TestFunc', got '%s'", result.Node.Name)
	}
	if !result.Node.IsExported {
		t.Error("expected Node.IsExported to be true")
	}
	if len(result.Highlights) != 2 {
		t.Errorf("expected 2 highlights, got %d", len(result.Highlights))
	}
}

func TestCallersResultParsing(t *testing.T) {
	raw := `{
		"symbol": "main.process",
		"callers": [
			{"name": "caller1", "kind": "function", "filePath": "/test/a.go", "startLine": 5},
			{"name": "caller2", "kind": "method", "filePath": "/test/b.go", "startLine": 15}
		]
	}`

	var result CallersResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("failed to unmarshal CallersResult: %v", err)
	}

	if result.Symbol != "main.process" {
		t.Errorf("expected Symbol 'main.process', got '%s'", result.Symbol)
	}
	if len(result.Callers) != 2 {
		t.Errorf("expected 2 callers, got %d", len(result.Callers))
	}
	if result.Callers[0].Name != "caller1" {
		t.Errorf("expected first caller name 'caller1', got '%s'", result.Callers[0].Name)
	}
	if result.Callers[0].StartLine != 5 {
		t.Errorf("expected first caller startLine 5, got %d", result.Callers[0].StartLine)
	}
}

func TestCalleeResultParsing(t *testing.T) {
	raw := `{
		"symbol": "main.compute",
		"callees": [
			{"name": "helper1", "kind": "function", "filePath": "/test/util.go", "startLine": 30}
		]
	}`

	var result CalleeResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("failed to unmarshal CalleeResult: %v", err)
	}

	if result.Symbol != "main.compute" {
		t.Errorf("expected Symbol 'main.compute', got '%s'", result.Symbol)
	}
	if len(result.Callees) != 1 {
		t.Errorf("expected 1 callee, got %d", len(result.Callees))
	}
	if result.Callees[0].Name != "helper1" {
		t.Errorf("expected callee name 'helper1', got '%s'", result.Callees[0].Name)
	}
}

func TestFileInfoParsing(t *testing.T) {
	raw := `{"path": "/test/main.go", "language": "go", "nodeCount": 150, "size": 2048}`

	var result FileInfo
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("failed to unmarshal FileInfo: %v", err)
	}

	if result.Path != "/test/main.go" {
		t.Errorf("expected Path '/test/main.go', got '%s'", result.Path)
	}
	if result.Language != "go" {
		t.Errorf("expected Language 'go', got '%s'", result.Language)
	}
	if result.NodeCount != 150 {
		t.Errorf("expected NodeCount 150, got %d", result.NodeCount)
	}
	if result.Size != 2048 {
		t.Errorf("expected Size 2048, got %d", result.Size)
	}
}

func TestContextResultParsing(t *testing.T) {
	raw := `{
		"query": "authentication",
		"subgraph": {
			"nodes": {"n1": {}},
			"edges": [{"source": "n1", "target": "n2", "kind": "calls"}],
			"roots": ["n1"]
		},
		"entryPoints": [{"id": "ep1", "name": "login", "kind": "function"}],
		"codeBlocks": [{"content": "func login() {}", "filePath": "/test/auth.go", "startLine": 1, "endLine": 5, "language": "go"}],
		"relatedFiles": ["/test/auth.go", "/test/session.go"],
		"summary": "Authentication handling",
		"stats": {"nodeCount": 10, "edgeCount": 15, "fileCount": 3, "codeBlockCount": 5, "totalCodeSize": 500}
	}`

	var result ContextResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("failed to unmarshal ContextResult: %v", err)
	}

	if result.Query != "authentication" {
		t.Errorf("expected Query 'authentication', got '%s'", result.Query)
	}
	if result.Subgraph == nil {
		t.Fatal("expected Subgraph to be non-nil")
	}
	if len(result.Subgraph.Edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(result.Subgraph.Edges))
	}
	if len(result.EntryPoints) != 1 {
		t.Errorf("expected 1 entry point, got %d", len(result.EntryPoints))
	}
	if len(result.CodeBlocks) != 1 {
		t.Errorf("expected 1 code block, got %d", len(result.CodeBlocks))
	}
	if result.Stats == nil {
		t.Fatal("expected Stats to be non-nil")
	}
	if result.Stats.NodeCount != 10 {
		t.Errorf("expected Stats.NodeCount 10, got %d", result.Stats.NodeCount)
	}
}

func TestContextStatsParsing(t *testing.T) {
	raw := `{"nodeCount": 100, "edgeCount": 200, "fileCount": 25, "codeBlockCount": 40, "totalCodeSize": 8000}`

	var result ContextStats
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("failed to unmarshal ContextStats: %v", err)
	}

	if result.NodeCount != 100 {
		t.Errorf("expected NodeCount 100, got %d", result.NodeCount)
	}
	if result.EdgeCount != 200 {
		t.Errorf("expected EdgeCount 200, got %d", result.EdgeCount)
	}
	if result.FileCount != 25 {
		t.Errorf("expected FileCount 25, got %d", result.FileCount)
	}
	if result.CodeBlockCount != 40 {
		t.Errorf("expected CodeBlockCount 40, got %d", result.CodeBlockCount)
	}
	if result.TotalCodeSize != 8000 {
		t.Errorf("expected TotalCodeSize 8000, got %d", result.TotalCodeSize)
	}
}