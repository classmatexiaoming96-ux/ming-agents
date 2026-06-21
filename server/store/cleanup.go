package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

// DefaultRetention is the default age threshold for cleaning up data belonging
// to terminal runs. Tasks and loop iterations for runs that ended more than this
// long ago are eligible for hard deletion.
const DefaultRetention = 7 * 24 * time.Hour

// CleanupConfig controls the retention cleanup of the append-only queue tables
// (agent_task_queue and loop_iterations).
//
// Background: both tables are written but never deleted at runtime — there is no
// TTL, no retention policy, and the only DELETE path is the FK cascade from runs,
// which nothing triggers. The JSONB columns (agent_request, agent_result,
// eval_details) accumulate full agent payloads, so the tables grow without bound.
// CleanupExpired performs a hard delete (Option B) of rows belonging to terminal
// runs older than the retention period, while never touching data for runs that
// are still pending/running/paused.
type CleanupConfig struct {
	// Retention is the minimum age (since a run ended) before its tasks and loop
	// iterations may be deleted. Values <= 0 fall back to DefaultRetention.
	Retention time.Duration
}

// DefaultCleanupConfig returns a CleanupConfig with the default 7-day retention.
func DefaultCleanupConfig() CleanupConfig {
	return CleanupConfig{Retention: DefaultRetention}
}

// retentionOrDefault returns the configured retention, or DefaultRetention when
// the configured value is non-positive.
func (c CleanupConfig) retentionOrDefault() time.Duration {
	if c.Retention <= 0 {
		return DefaultRetention
	}
	return c.Retention
}

// cutoffTime computes the timestamp before which a terminal run's data is
// eligible for deletion, relative to now.
func (c CleanupConfig) cutoffTime(now time.Time) time.Time {
	return now.Add(-c.retentionOrDefault())
}

// CleanupResult reports how much data a cleanup pass removed.
type CleanupResult struct {
	// Cutoff is the threshold used; only rows for terminal runs that ended before
	// this time were deleted.
	Cutoff time.Time `json:"cutoff"`
	// TasksDeleted is the number of agent_task_queue rows removed.
	TasksDeleted int64 `json:"tasks_deleted"`
	// LoopIterationsDeleted is the number of loop_iterations rows removed.
	LoopIterationsDeleted int64 `json:"loop_iterations_deleted"`
}

// execer is the subset of *sql.DB / *sql.Tx used by the cleanup deletes. It lets
// the SQL be unit-tested with a fake and run within or without a transaction.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Terminal run statuses whose queue/iteration data is safe to delete. Runs that
// are pending, running, or paused are deliberately excluded so in-flight work is
// never disturbed. Keep this list in sync with the SQL predicates below.
//
// COALESCE(ended_at, updated_at) is used as the run's "end time": ended_at is set
// when a run reaches a terminal state, and updated_at is a safe fallback for older
// rows where ended_at may be NULL.
const deleteTasksSQL = `DELETE FROM agent_task_queue t USING runs r
	WHERE t.run_id = r.id
	  AND r.status IN ('completed','failed','cancelled')
	  AND COALESCE(r.ended_at, r.updated_at) < $1`

const deleteLoopIterationsSQL = `DELETE FROM loop_iterations li USING runs r
	WHERE li.run_id = r.id
	  AND r.status IN ('completed','failed','cancelled')
	  AND COALESCE(r.ended_at, r.updated_at) < $1`

// cleanupExpired runs the two retention deletes against ex using the given cutoff
// and aggregates the affected-row counts. It is the testable core of
// CleanupExpired.
func cleanupExpired(ex execer, cutoff time.Time) (CleanupResult, error) {
	result := CleanupResult{Cutoff: cutoff}

	tr, err := ex.Exec(deleteTasksSQL, cutoff)
	if err != nil {
		return result, fmt.Errorf("delete expired tasks: %w", err)
	}
	result.TasksDeleted, _ = tr.RowsAffected()

	lr, err := ex.Exec(deleteLoopIterationsSQL, cutoff)
	if err != nil {
		return result, fmt.Errorf("delete expired loop iterations: %w", err)
	}
	result.LoopIterationsDeleted, _ = lr.RowsAffected()

	return result, nil
}

// CleanupExpired hard-deletes agent_task_queue and loop_iterations rows belonging
// to terminal runs (completed/failed/cancelled) that ended before now minus the
// configured retention period. Data for pending/running/paused runs is never
// touched. Both deletes run in a single transaction so the result is all-or-nothing.
func (s *Store) CleanupExpired(cfg CleanupConfig) (CleanupResult, error) {
	cutoff := cfg.cutoffTime(Now())
	var result CleanupResult
	err := s.WithTx(func(tx *Tx) error {
		r, err := cleanupExpired(tx.Tx, cutoff)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return CleanupResult{Cutoff: cutoff}, err
	}
	return result, nil
}

// RunPeriodicCleanup runs CleanupExpired on a fixed interval until ctx is
// cancelled. It is a convenience for wiring background retention into a long-lived
// process; manual one-shot cleanup is available via Store.CleanupExpired (CLI/API).
// Cleanup errors are logged and do not stop the loop.
func (s *Store) RunPeriodicCleanup(ctx context.Context, cfg CleanupConfig, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			res, err := s.CleanupExpired(cfg)
			if err != nil {
				log.Printf("[cleanup] failed: %v", err)
				continue
			}
			log.Printf("[cleanup] removed %d tasks, %d loop iterations (cutoff %s)",
				res.TasksDeleted, res.LoopIterationsDeleted, res.Cutoff.Format(time.RFC3339))
		}
	}
}
