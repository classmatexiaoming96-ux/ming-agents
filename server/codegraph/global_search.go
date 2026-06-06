package codegraph

import (
	"context"
	"sync"
)

// GlobalSearchResult aggregates search results from multiple repositories.
type GlobalSearchResult struct {
	Results []RepoSearchResult `json:"results"`
	Errors  []SearchError      `json:"errors,omitempty"`
}

// RepoSearchResult pairs a repo ID with its search result.
type RepoSearchResult struct {
	RepoID string `json:"repoID"`
	Result SearchResult `json:"result"`
}

// SearchError records a search failure for a specific repo.
type SearchError struct {
	RepoID string `json:"repoID"`
	Err    string `json:"error"`
}

// GlobalSearch fans out a query to multiple repositories in parallel.
func (c *CodeGraphCLI) GlobalSearch(ctx context.Context, query string, repoIDs []string) (*GlobalSearchResult, error) {
	if len(repoIDs) == 0 {
		return &GlobalSearchResult{}, nil
	}

	type result struct {
		repoID string
		items  []SearchResult
		err    error
	}

	resultCh := make(chan result, len(repoIDs))
	var wg sync.WaitGroup

	for _, repoID := range repoIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			repoPath, err := c.pool.resolveRepoPath(id)
			if err != nil {
				resultCh <- result{repoID: id, err: err}
				return
			}

			items, err := c.Query(ctx, repoPath, query, nil)
			resultCh <- result{repoID: id, items: items, err: err}
		}(repoID)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	agg := &GlobalSearchResult{
		Results: make([]RepoSearchResult, 0),
	}

	for r := range resultCh {
		if r.err != nil {
			agg.Errors = append(agg.Errors, SearchError{
				RepoID: r.repoID,
				Err:    r.err.Error(),
			})
			continue
		}

		for _, item := range r.items {
			agg.Results = append(agg.Results, RepoSearchResult{
				RepoID: r.repoID,
				Result: item,
			})
		}
	}

	return agg, nil
}

// resolveRepoPath resolves a repo ID to its path (temporary helper until registry integration).
func (p *ProcessPool) resolveRepoPath(repoID string) (string, error) {
	return "", nil // Will be connected to registry
}