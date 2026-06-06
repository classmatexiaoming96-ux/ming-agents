package codegraph

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RepoConfig holds configuration for a registered repository.
type RepoConfig struct {
	RepoID  string `json:"repoID"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Label   string `json:"label"`
}

// RepoRegistry manages the mapping of repo IDs to their configurations.
type RepoRegistry struct {
	mu sync.RWMutex
	repos map[string]*RepoConfig
}

// NewRepoRegistry creates a new repository registry.
func NewRepoRegistry() *RepoRegistry {
	return &RepoRegistry{
		repos: make(map[string]*RepoConfig),
	}
}

// RegisterRepo adds a repository to the registry.
func (r *RepoRegistry) RegisterRepo(repoID, path, name, label string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}

	if !IsInitialized(absPath) {
		return fmt.Errorf("repository not initialized: %s", absPath)
	}

	r.repos[repoID] = &RepoConfig{
		RepoID: repoID,
		Path:   absPath,
		Name:   name,
		Label:  label,
	}

	return nil
}

// UnregisterRepo removes a repository from the registry.
func (r *RepoRegistry) UnregisterRepo(repoID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.repos[repoID]; !exists {
		return fmt.Errorf("unknown repo: %s", repoID)
	}

	delete(r.repos, repoID)
	return nil
}

// ResolveRepoPath returns the absolute path for a repo ID.
func (r *RepoRegistry) ResolveRepoPath(repoID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	repo, exists := r.repos[repoID]
	if !exists {
		return "", fmt.Errorf("unknown repo: %s", repoID)
	}

	return repo.Path, nil
}

// ListRepos returns all registered repositories.
func (r *RepoRegistry) ListRepos() []RepoConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	repos := make([]RepoConfig, 0, len(r.repos))
	for _, repo := range r.repos {
		repos = append(repos, *repo)
	}
	return repos
}

// GetRepo returns the config for a specific repo.
func (r *RepoRegistry) GetRepo(repoID string) (*RepoConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	repo, exists := r.repos[repoID]
	if !exists {
		return nil, fmt.Errorf("unknown repo: %s", repoID)
	}
	return repo, nil
}

// IsInitialized checks if a path has an initialized .codegraph/ database.
func IsInitialized(path string) bool {
	dbPath := filepath.Join(path, ".codegraph", "codegraph.db")
	_, err := os.Stat(dbPath)
	return err == nil
}