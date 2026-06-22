package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
)

type taskRepo struct{ s *Store }

func (r taskRepo) Create(t *domain.Task) error {
	t.ID = uuid.New()
	t.CreatedAt = Now()
	q := `INSERT INTO agent_task_queue(id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,created_at,version)
	      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,1)`
	_, err := r.s.db.Exec(q, t.ID, t.RunID, t.StepID, t.Iteration, t.Attempt,
		t.Status, t.AdapterKey, t.AgentRequest, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func (r taskRepo) CreateMany(tasks []*domain.Task) error {
	return r.s.WithTx(func(tx *Tx) error {
		q := `INSERT INTO agent_task_queue(id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,created_at,version)
		      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,1)`
		for _, t := range tasks {
			t.ID = uuid.New()
			t.CreatedAt = Now()
			if _, err := tx.Tx.Exec(q, t.ID, t.RunID, t.StepID, t.Iteration, t.Attempt,
				t.Status, t.AdapterKey, t.AgentRequest, t.CreatedAt); err != nil {
				return fmt.Errorf("create task: %w", err)
			}
		}
		return nil
	})
}

func (r taskRepo) Get(id uuid.UUID) (*domain.Task, error) {
	q := `SELECT id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,agent_result,result_summary,claimed_at,completed_at,created_at,version
	      FROM agent_task_queue WHERE id=$1`
	var t domain.Task
	err := r.s.db.QueryRow(q, id).Scan(
		&t.ID, &t.RunID, &t.StepID, &t.Iteration, &t.Attempt, &t.Status,
		&t.AdapterKey, &t.AgentRequest, &t.AgentResult, &t.ResultSummary,
		&t.ClaimedAt, &t.CompletedAt, &t.CreatedAt, &t.Version)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return &t, nil
}

func (r taskRepo) Update(t *domain.Task) error {
	q := `UPDATE agent_task_queue SET status=$2,agent_result=$3,result_summary=$4,claimed_at=$5,completed_at=$6,version=version+1
	      WHERE id=$1 AND version=$7`
	res, err := r.s.db.Exec(q, t.ID, t.Status, t.AgentResult, t.ResultSummary,
		t.ClaimedAt, t.CompletedAt, t.Version)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("optimistic lock failed for task %s", t.ID)
	}
	t.Version++
	return nil
}

// Claim atomically finds and claims one pending task.
// Uses SELECT FOR UPDATE SKIP LOCKED to avoid contention.
func (r taskRepo) Claim() (*domain.Task, error) {
	now := time.Now().UTC()
	q := `UPDATE agent_task_queue SET status='claimed',claimed_at=$1,version=version+1
	      WHERE id=(SELECT id FROM agent_task_queue WHERE status='pending' ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED)
	      RETURNING id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,agent_result,result_summary,claimed_at,completed_at,created_at,version`
	fallback := `UPDATE agent_task_queue SET status='claimed',claimed_at=$1,version=version+1
	      WHERE id=(SELECT id FROM agent_task_queue WHERE status='pending' ORDER BY created_at ASC LIMIT 1)
	      RETURNING id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,agent_result,result_summary,claimed_at,completed_at,created_at,version`
	return r.claimWithQuery(q, fallback, now)
}

// ClaimForAdapter atomically claims one pending task for a specific adapter.
func (r taskRepo) ClaimForAdapter(adapterKey string) (*domain.Task, error) {
	now := time.Now().UTC()
	q := `UPDATE agent_task_queue SET status='claimed',claimed_at=$1,version=version+1
	      WHERE id=(SELECT id FROM agent_task_queue WHERE status='pending' AND adapter_key=$2 ORDER BY created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED)
	      RETURNING id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,agent_result,result_summary,claimed_at,completed_at,created_at,version`
	fallback := `UPDATE agent_task_queue SET status='claimed',claimed_at=$1,version=version+1
	      WHERE id=(SELECT id FROM agent_task_queue WHERE status='pending' AND adapter_key=$2 ORDER BY created_at ASC LIMIT 1)
	      RETURNING id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,agent_result,result_summary,claimed_at,completed_at,created_at,version`
	return r.claimWithQuery(q, fallback, now, adapterKey)
}

func (r taskRepo) claimWithQuery(q, fallback string, args ...any) (*domain.Task, error) {
	var t domain.Task
	err := r.s.db.QueryRow(q, args...).Scan(
		&t.ID, &t.RunID, &t.StepID, &t.Iteration, &t.Attempt, &t.Status,
		&t.AdapterKey, &t.AgentRequest, &t.AgentResult, &t.ResultSummary,
		&t.ClaimedAt, &t.CompletedAt, &t.CreatedAt, &t.Version)
	if err != nil && strings.Contains(err.Error(), `near "FOR"`) {
		err = r.s.db.QueryRow(fallback, args...).Scan(
			&t.ID, &t.RunID, &t.StepID, &t.Iteration, &t.Attempt, &t.Status,
			&t.AdapterKey, &t.AgentRequest, &t.AgentResult, &t.ResultSummary,
			&t.ClaimedAt, &t.CompletedAt, &t.CreatedAt, &t.Version)
	}
	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	return &t, nil
}

func (r taskRepo) ByRun(runID uuid.UUID) ([]*domain.Task, error) {
	q := `SELECT id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,agent_result,result_summary,claimed_at,completed_at,created_at,version
	      FROM agent_task_queue WHERE run_id=$1 ORDER BY created_at ASC`
	return r.queryMany(q, runID)
}

func (r taskRepo) ByStep(stepID uuid.UUID) ([]*domain.Task, error) {
	q := `SELECT id,run_id,step_id,iteration,attempt,status,adapter_key,agent_request,agent_result,result_summary,claimed_at,completed_at,created_at,version
	      FROM agent_task_queue WHERE step_id=$1 ORDER BY created_at ASC`
	return r.queryMany(q, stepID)
}

func (r taskRepo) queryMany(q string, arg any) ([]*domain.Task, error) {
	rows, err := r.s.db.Query(q, arg)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()
	var tasks []*domain.Task
	for rows.Next() {
		var t domain.Task
		err := rows.Scan(&t.ID, &t.RunID, &t.StepID, &t.Iteration, &t.Attempt, &t.Status,
			&t.AdapterKey, &t.AgentRequest, &t.AgentResult, &t.ResultSummary,
			&t.ClaimedAt, &t.CompletedAt, &t.CreatedAt, &t.Version)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, rows.Err()
}

// ClaimedCount returns how many tasks are currently claimed for a run.
func (r taskRepo) ClaimedCount(runID uuid.UUID) (int, error) {
	q := `SELECT COUNT(*) FROM agent_task_queue WHERE run_id=$1 AND status='claimed'`
	var n int
	err := r.s.db.QueryRow(q, runID).Scan(&n)
	return n, err
}

// PendingCount returns how many tasks are pending for a run.
func (r taskRepo) PendingCount(runID uuid.UUID) (int, error) {
	q := `SELECT COUNT(*) FROM agent_task_queue WHERE run_id=$1 AND status='pending'`
	var n int
	err := r.s.db.QueryRow(q, runID).Scan(&n)
	return n, err
}

// UpdateStatus updates task status with no version check (for workers).
func (r taskRepo) UpdateStatus(id uuid.UUID, to domain.TaskStatus) error {
	now := time.Now().UTC()
	var q string
	switch to {
	case domain.TaskStatusClaimed:
		q = `UPDATE agent_task_queue SET status=$2,claimed_at=$3,version=version+1 WHERE id=$1`
		_, err := r.s.db.Exec(q, id, to, now)
		return err
	case domain.TaskStatusCompleted, domain.TaskStatusFailed:
		q = `UPDATE agent_task_queue SET status=$2,completed_at=$3,version=version+1 WHERE id=$1`
		_, err := r.s.db.Exec(q, id, to, now)
		return err
	default:
		q = `UPDATE agent_task_queue SET status=$2,version=version+1 WHERE id=$1`
		_, err := r.s.db.Exec(q, id, to)
		return err
	}
}

// SetResult writes the agent result back to a task.
func (r taskRepo) SetResult(id uuid.UUID, result json.RawMessage, summary string, status domain.TaskStatus) error {
	now := time.Now().UTC()
	q := `UPDATE agent_task_queue SET status=$2,agent_result=$3,result_summary=$4,completed_at=$5,version=version+1 WHERE id=$1`
	_, err := r.s.db.Exec(q, id, status, result, summary, now)
	return err
}
