package codegraph

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"
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

	agg := &GlobalSearchResult{
		Results: make([]RepoSearchResult, 0),
		Errors:  make([]SearchError, 0),
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(10) // limit concurrent repo searches

	for _, repoID := range repoIDs {
		repoID := repoID // capture loop variable
		g.Go(func() error {
			// 30s timeout per repo
			repoCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			repoPath, err := c.pool.resolveRepoPath(repoID)
			if err != nil {
				agg.Errors = append(agg.Errors, SearchError{RepoID: repoID, Err: err.Error()})
				return nil // don't propagate - we collect errors
			}

			items, err := c.Query(repoCtx, repoPath, query, nil)
			if err != nil {
				agg.Errors = append(agg.Errors, SearchError{RepoID: repoID, Err: err.Error()})
				return nil
			}

			for _, item := range items {
				agg.Results = append(agg.Results, RepoSearchResult{
					RepoID: repoID,
					Result: item,
				})
			}
			return nil
		})
	}

	_ = g.Wait() // errors collected in agg.Errors
	return agg, nil
}

// resolveRepoPath resolves a repo ID to its path (temporary helper until registry integration).
func (p *ProcessPool) resolveRepoPath(repoID string) (string, error) {
	return "", nil // Will be connected to registry
}