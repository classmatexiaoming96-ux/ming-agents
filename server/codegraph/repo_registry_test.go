package codegraph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoRegistryRegisterAndResolve(t *testing.T) {
	// Create a temp directory with a .codegraph subfolder to simulate an initialized repo
	tmpDir := t.TempDir()
	codegraphDir := filepath.Join(tmpDir, ".codegraph")
	if err := os.MkdirAll(codegraphDir, 0755); err != nil {
		t.Fatalf("failed to create .codegraph dir: %v", err)
	}
	// Create the db file so IsInitialized returns true
	dbPath := filepath.Join(codegraphDir, "codegraph.db")
	if err := os.WriteFile(dbPath, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create db file: %v", err)
	}

	registry := NewRepoRegistry()

	// Test registering a repo
	err := registry.RegisterRepo("repo1", tmpDir, "test-repo", "Test Repository")
	if err != nil {
		t.Fatalf("RegisterRepo failed: %v", err)
	}

	// Test resolving the path
	resolvedPath, err := registry.ResolveRepoPath("repo1")
	if err != nil {
		t.Fatalf("ResolveRepoPath failed: %v", err)
	}
	if resolvedPath != tmpDir {
		t.Errorf("expected resolved path '%s', got '%s'", tmpDir, resolvedPath)
	}

	// Test GetRepo
	repo, err := registry.GetRepo("repo1")
	if err != nil {
		t.Fatalf("GetRepo failed: %v", err)
	}
	if repo.RepoID != "repo1" {
		t.Errorf("expected RepoID 'repo1', got '%s'", repo.RepoID)
	}
	if repo.Name != "test-repo" {
		t.Errorf("expected Name 'test-repo', got '%s'", repo.Name)
	}
}

func TestRepoRegistryUnregister(t *testing.T) {
	tmpDir := t.TempDir()
	codegraphDir := filepath.Join(tmpDir, ".codegraph")
	if err := os.MkdirAll(codegraphDir, 0755); err != nil {
		t.Fatalf("failed to create .codegraph dir: %v", err)
	}
	dbPath := filepath.Join(codegraphDir, "codegraph.db")
	if err := os.WriteFile(dbPath, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create db file: %v", err)
	}

	registry := NewRepoRegistry()

	registry.RegisterRepo("repo1", tmpDir, "test-repo", "Test")

	// Unregister should succeed
	err := registry.UnregisterRepo("repo1")
	if err != nil {
		t.Fatalf("UnregisterRepo failed: %v", err)
	}

	// Resolving unregistered repo should fail
	_, err = registry.ResolveRepoPath("repo1")
	if err == nil {
		t.Error("expected error for unregistered repo, got nil")
	}
}

func TestRepoRegistryListRepos(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	for _, dir := range []string{tmpDir1, tmpDir2} {
		codegraphDir := filepath.Join(dir, ".codegraph")
		if err := os.MkdirAll(codegraphDir, 0755); err != nil {
			t.Fatalf("failed to create .codegraph dir: %v", err)
		}
		dbPath := filepath.Join(codegraphDir, "codegraph.db")
		if err := os.WriteFile(dbPath, []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create db file: %v", err)
		}
	}

	registry := NewRepoRegistry()

	registry.RegisterRepo("repo1", tmpDir1, "repo-one", "Repo One")
	registry.RegisterRepo("repo2", tmpDir2, "repo-two", "Repo Two")

	repos := registry.ListRepos()
	if len(repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(repos))
	}
}

func TestRepoRegistryResolveUnknownRepo(t *testing.T) {
	registry := NewRepoRegistry()

	_, err := registry.ResolveRepoPath("nonexistent")
	if err == nil {
		t.Error("expected error for unknown repo, got nil")
	}
}

func TestRepoRegistryGetUnknownRepo(t *testing.T) {
	registry := NewRepoRegistry()

	_, err := registry.GetRepo("nonexistent")
	if err == nil {
		t.Error("expected error for unknown repo, got nil")
	}
}

func TestIsInitialized(t *testing.T) {
	// Test with non-existent path
	if IsInitialized("/nonexistent/path") {
		t.Error("expected false for non-existent path")
	}

	// Test with initialized repo
	tmpDir := t.TempDir()
	codegraphDir := filepath.Join(tmpDir, ".codegraph")
	if err := os.MkdirAll(codegraphDir, 0755); err != nil {
		t.Fatalf("failed to create .codegraph dir: %v", err)
	}
	dbPath := filepath.Join(codegraphDir, "codegraph.db")
	if err := os.WriteFile(dbPath, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create db file: %v", err)
	}

	if !IsInitialized(tmpDir) {
		t.Error("expected true for initialized repo")
	}
}

func TestRegisterRepoInvalidPath(t *testing.T) {
	registry := NewRepoRegistry()

	// Test registering with non-initialized path
	err := registry.RegisterRepo("repo1", "/nonexistent/path", "test", "Test")
	if err == nil {
		t.Error("expected error for non-initialized path, got nil")
	}
}