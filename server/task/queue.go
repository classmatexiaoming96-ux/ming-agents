package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoTask is returned by Claim when no pending task is available.
var ErrNoTask = errors.New("no task available")

// Queue is the Postgres-backed task queue. All operations are safe for
// concurrent use and rely on row-level locks for correctness.
type Queue struct {
	pool *pgxpool.Pool
}

func NewQueue(pool *pgxpool.Pool) *Queue { return &Queue{pool: pool} }

// the column list shared by every SELECT so scans stay in sync.
const taskColumns = `id, agent_id, status, priority, prompt, result, error,
	worker_id, attempts, max_attempts, cancel_requested,
	created_at, claimed_at, started_at, heartbeat_at, completed_at`

func scanTask(row pgx.Row) (*Task, error) {
	var t Task
	err := row.Scan(
		&t.ID, &t.AgentID, &t.Status, &t.Priority, &t.Prompt, &t.Result, &t.Error,
		&t.WorkerID, &t.Attempts, &t.MaxAttempts, &t.CancelRequested,
		&t.CreatedAt, &t.ClaimedAt, &t.StartedAt, &t.HeartbeatAt, &t.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Enqueue inserts a new pending task and returns it.
func (q *Queue) Enqueue(ctx context.Context, agentID int64, prompt string, priority int) (*Task, error) {
	if priority < PriorityHigh || priority > PriorityLow {
		priority = PriorityMedium
	}
	row := q.pool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, prompt, priority)
		VALUES ($1, $2, $3)
		RETURNING `+taskColumns, agentID, prompt, priority)
	return scanTask(row)
}

// Claim atomically grabs the highest-priority pending task for one of the given
// agents and marks it claimed by workerID. Returns ErrNoTask if none available.
//
// FOR UPDATE SKIP LOCKED makes this safe under concurrent claims (multiple
// goroutines and, in the future, multiple daemons).
func (q *Queue) Claim(ctx context.Context, workerID string, agentIDs []int64) (*Task, error) {
	if len(agentIDs) == 0 {
		return nil, ErrNoTask
	}
	row := q.pool.QueryRow(ctx, `
		UPDATE agent_task_queue SET
			status       = 'claimed',
			worker_id    = $1,
			attempts     = attempts + 1,
			claimed_at   = now(),
			heartbeat_at = now()
		WHERE id = (
			SELECT id FROM agent_task_queue
			WHERE status = 'pending' AND agent_id = ANY($2)
			  AND (max_attempts IS NULL OR attempts < max_attempts)
			ORDER BY priority ASC, created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+taskColumns, workerID, agentIDs)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoTask
	}
	return t, err
}

// MarkRunning transitions a claimed task to running.
func (q *Queue) MarkRunning(ctx context.Context, id int64) error {
	return q.exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now(), heartbeat_at = now()
		WHERE id = $1`, id)
}

// Heartbeat refreshes liveness for a running task owned by workerID. Returns
// whether a cancel has been requested for this task so the runner can react.
func (q *Queue) Heartbeat(ctx context.Context, id int64, workerID string) (cancelRequested bool, err error) {
	row := q.pool.QueryRow(ctx, `
		UPDATE agent_task_queue
		SET heartbeat_at = now()
		WHERE id = $1 AND worker_id = $2 AND status IN ('claimed','running')
		RETURNING cancel_requested`, id, workerID)
	err = row.Scan(&cancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		// task is no longer ours / already terminal — treat as cancel signal.
		return true, nil
	}
	return cancelRequested, err
}

// Complete marks a task completed with its result.
func (q *Queue) Complete(ctx context.Context, id int64, result string) error {
	return q.exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', result = $2, completed_at = now()
		WHERE id = $1`, id, result)
}

// Fail marks a task failed with an error message. The partial result (if any)
// is preserved for debugging.
func (q *Queue) Fail(ctx context.Context, id int64, errMsg, partial string) error {
	return q.exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'failed', error = $2, result = $3, completed_at = now()
		WHERE id = $1`, id, errMsg, partial)
}

// Canceled marks a task canceled.
func (q *Queue) Canceled(ctx context.Context, id int64, partial string) error {
	return q.exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'canceled', result = $2, completed_at = now()
		WHERE id = $1`, id, partial)
}

// RequestCancel flags a task for cancellation. Used by the API; the daemon
// reacts either via the in-process Manager or on the next heartbeat.
func (q *Queue) RequestCancel(ctx context.Context, id int64) (*Task, error) {
	row := q.pool.QueryRow(ctx, `
		UPDATE agent_task_queue
		SET cancel_requested = true
		WHERE id = $1 AND status IN ('pending','claimed','running')
		RETURNING `+taskColumns, id)
	t, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("task %d not cancelable", id)
	}
	return t, err
}

// RecoverOrphanedTasks resets tasks whose owning worker died (stale heartbeat)
// back to pending so they can be re-claimed. Returns the number recovered.
func (q *Queue) RecoverOrphanedTasks(ctx context.Context, timeout time.Duration) (int, error) {
	tag, err := q.pool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status       = 'pending',
		    worker_id    = NULL,
		    claimed_at   = NULL,
		    started_at   = NULL,
		    heartbeat_at = NULL
		WHERE status IN ('claimed','running')
		  AND (heartbeat_at IS NULL OR heartbeat_at < now() - make_interval(secs => $1))`,
		timeout.Seconds())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Get returns a single task by id.
func (q *Queue) Get(ctx context.Context, id int64) (*Task, error) {
	return scanTask(q.pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM agent_task_queue WHERE id = $1`, id))
}

// List returns the most recent tasks (newest first), capped at limit.
func (q *Queue) List(ctx context.Context, limit int) ([]*Task, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := q.pool.Query(ctx, `SELECT `+taskColumns+`
		FROM agent_task_queue ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountInflight returns how many non-terminal tasks an agent currently has,
// used by the scheduler to compute free slots after a restart.
func (q *Queue) CountInflight(ctx context.Context, agentID int64, workerID string) (int, error) {
	var n int
	err := q.pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue
		WHERE agent_id = $1 AND worker_id = $2 AND status IN ('claimed','running')`,
		agentID, workerID).Scan(&n)
	return n, err
}

func (q *Queue) exec(ctx context.Context, sql string, args ...any) error {
	_, err := q.pool.Exec(ctx, sql, args...)
	return err
}
