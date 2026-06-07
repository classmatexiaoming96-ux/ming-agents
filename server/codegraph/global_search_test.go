package codegraph

import (
	"context"
	"testing"
	"time"
)

func TestGlobalSearchResultStructure(t *testing.T) {
	result := GlobalSearchResult{
		Results: []RepoSearchResult{
			{RepoID: "repo1", Result: SearchResult{Score: 0.9}},
			{RepoID: "repo2", Result: SearchResult{Score: 0.8}},
		},
		Errors: []SearchError{
			{RepoID: "repo3", Err: "connection refused"},
		},
	}

	if len(result.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(result.Results))
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
	if result.Errors[0].RepoID != "repo3" {
		t.Errorf("expected error repo ID 'repo3', got '%s'", result.Errors[0].RepoID)
	}
}

func TestGlobalSearchEmptyRepoIDs(t *testing.T) {
	cli := &CodeGraphCLI{
		binaryPath: "codegraph",
		pool:       NewProcessPool(2, 5*time.Minute),
	}

	result, err := cli.GlobalSearch(context.Background(), "test query", []string{})
	if err != nil {
		t.Fatalf("GlobalSearch failed: %v", err)
	}
	if len(result.Results) != 0 {
		t.Errorf("expected 0 results for empty repo IDs, got %d", len(result.Results))
	}
	if result.Errors != nil && len(result.Errors) != 0 {
		t.Errorf("expected no errors for empty repo IDs, got %d", len(result.Errors))
	}
}

func TestRepoSearchResultStructure(t *testing.T) {
	result := RepoSearchResult{
		RepoID: "my-repo",
		Result: SearchResult{
			Node: Node{
				ID:   "node1",
				Kind: "function",
				Name: "testFunc",
			},
			Score: 0.75,
		},
	}

	if result.RepoID != "my-repo" {
		t.Errorf("expected RepoID 'my-repo', got '%s'", result.RepoID)
	}
	if result.Result.Score != 0.75 {
		t.Errorf("expected Score 0.75, got %f", result.Result.Score)
	}
	if result.Result.Node.Name != "testFunc" {
		t.Errorf("expected Node.Name 'testFunc', got '%s'", result.Result.Node.Name)
	}
}

func TestSearchErrorStructure(t *testing.T) {
	err := SearchError{
		RepoID: "broken-repo",
		Err:    "timeout",
	}

	if err.RepoID != "broken-repo" {
		t.Errorf("expected RepoID 'broken-repo', got '%s'", err.RepoID)
	}
	if err.Err != "timeout" {
		t.Errorf("expected Err 'timeout', got '%s'", err.Err)
	}
}