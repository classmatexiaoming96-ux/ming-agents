// Package task contains the Postgres-backed work queue, the exec.Command runner
// that drives Claude Code, and the per-task cancellation registry.
package task

import "time"

// Status values for agent_task_queue.status.
const (
	StatusPending   = "pending"
	StatusClaimed   = "claimed"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

// Priority values (lower number = higher priority; claim orders priority ASC).
const (
	PriorityHigh   = 1
	PriorityMedium = 2
	PriorityLow    = 3
)

// Task mirrors a row of agent_task_queue.
type Task struct {
	ID              int64      `json:"id"`
	RepoPath        string     `json:"repo_path,omitempty"`
	AgentID         int64      `json:"agent_id"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	Prompt          string     `json:"prompt"`
	Result          *string    `json:"result,omitempty"`
	Error           *string    `json:"error,omitempty"`
	WorkerID        *string    `json:"worker_id,omitempty"`
	Attempts        int        `json:"attempts"`
	MaxAttempts     int        `json:"max_attempts"`
	CancelRequested bool       `json:"cancel_requested"`
	CreatedAt       time.Time  `json:"created_at"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	HeartbeatAt     *time.Time `json:"heartbeat_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}
