package domain

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// TaskStatus represents the status of a Task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusClaimed    TaskStatus = "claimed"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusFailed     TaskStatus = "failed"
)

// Task is a unit of work produced by a Step fan-out, consumed from agent_task_queue.
type Task struct {
	ID             uuid.UUID       `json:"id"`
	RunID          uuid.UUID       `json:"run_id"`
	StepID         uuid.UUID       `json:"step_id"`
	Iteration      int             `json:"iteration"`
	Attempt        int             `json:"attempt"`
	Status         TaskStatus      `json:"status"`
	AdapterKey     string          `json:"adapter_key"`
	AgentRequest   json.RawMessage `json:"agent_request"`
	AgentResult    json.RawMessage `json:"agent_result,omitempty"`
	ResultSummary  sql.NullString  `json:"-"`
	ResultSummaryStr string        `json:"result_summary,omitempty"`
	ClaimedAt      sql.NullTime   `json:"-"`
	ClaimedAtStr   *string         `json:"claimed_at,omitempty"`
	CompletedAt    sql.NullTime   `json:"-"`
	CompletedAtStr *string        `json:"completed_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	Version        int            `json:"version"`
}

// MarshalJSON custom marshaler.
func (t Task) MarshalJSON() ([]byte, error) {
type Alias Task
		aux := struct {
			Alias
			ResultSummaryStr string  `json:"result_summary,omitempty"`
			ClaimedAtStr     *string `json:"claimed_at,omitempty"`
			CompletedAtStr   *string `json:"completed_at,omitempty"`
		}{
			Alias: Alias(t),
		}
	if aux.ResultSummary.Valid = t.ResultSummary.Valid; aux.ResultSummary.Valid {
		aux.ResultSummaryStr = t.ResultSummary.String
	}
	if aux.ClaimedAt.Valid = t.ClaimedAt.Valid; aux.ClaimedAt.Valid {
		aux.ClaimedAtStr = stringPtr(t.ClaimedAt.Time.Format(time.RFC3339))
	}
	if aux.CompletedAt.Valid = t.CompletedAt.Valid; aux.CompletedAt.Valid {
		aux.CompletedAtStr = stringPtr(t.CompletedAt.Time.Format(time.RFC3339))
	}
	return json.Marshal(aux)
}

func stringPtr(s string) *string { return &s }

// LoopIteration tracks iteration state for a loop step.
type LoopIteration struct {
	ID          uuid.UUID      `json:"id"`
	RunID       uuid.UUID      `json:"run_id"`
	StepID      uuid.UUID      `json:"step_id"`
	Iteration   int            `json:"iteration"`
	Status      string         `json:"status"`
	EvalScore   *float64       `json:"eval_score,omitempty"`
	EvalDetails json.RawMessage `json:"eval_details,omitempty"`
	Converged   bool           `json:"converged"`
	CreatedAt   time.Time      `json:"created_at"`
}

// IterationStatus constants.
const (
	IterationStatusRunning       = "running"
	IterationStatusConverged     = "converged"
	IterationStatusMaxIterations = "max_iterations"
	IterationStatusFailed       = "failed"
	IterationStatusNoProgress   = "no_progress" // Epic 3.4: no improvement detected
)