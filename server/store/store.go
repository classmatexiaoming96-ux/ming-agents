package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/ming-agents/server/domain"
)

// Store manages all persistence operations.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store backed by the given DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// BeginTx starts a transaction.
func (s *Store) BeginTx() (*sql.Tx, error) {
	return s.db.Begin()
}

// DB returns the underlying *sql.DB.
func (s *Store) DB() *sql.DB { return s.db }

// ─── Run CRUD ────────────────────────────────────────────────────────────────

// CreateRun inserts a new run.
func (s *Store) CreateRun(r *domain.Run) error {
	return runRepo{s}.Create(r)
}

// GetRun fetches a run by ID.
func (s *Store) GetRun(id uuid.UUID) (*domain.Run, error) {
	return runRepo{s}.Get(id)
}

// UpdateRun updates a run with optimistic locking.
func (s *Store) UpdateRun(r *domain.Run) error {
	return runRepo{s}.Update(r)
}

// ListRuns returns all runs (for API listing).
func (s *Store) ListRuns(limit, offset int) ([]*domain.Run, error) {
	return runRepo{s}.List(limit, offset)
}

// ─── Step CRUD ───────────────────────────────────────────────────────────────

// CreateStep inserts a step.
func (s *Store) CreateStep(step *domain.Step) error {
	return stepRepo{s}.Create(step)
}

// GetStep fetches a step by ID.
func (s *Store) GetStep(id uuid.UUID) (*domain.Step, error) {
	return stepRepo{s}.Get(id)
}

// UpdateStep updates a step.
func (s *Store) UpdateStep(step *domain.Step) error {
	return stepRepo{s}.Update(step)
}

// GetStepsByRun returns all steps for a run.
func (s *Store) GetStepsByRun(runID uuid.UUID) ([]*domain.Step, error) {
	return stepRepo{s}.ByRun(runID)
}

// ─── Task CRUD ────────────────────────────────────────────────────────────────

// CreateTask inserts a task.
func (s *Store) CreateTask(t *domain.Task) error {
	return taskRepo{s}.Create(t)
}

// GetTask fetches a task by ID.
func (s *Store) GetTask(id uuid.UUID) (*domain.Task, error) {
	return taskRepo{s}.Get(id)
}

// UpdateTask updates a task.
func (s *Store) UpdateTask(t *domain.Task) error {
	return taskRepo{s}.Update(t)
}

// ClaimTask atomically claims a pending task for a worker.
// Returns the claimed task or sql.ErrNoRows if none available.
func (s *Store) ClaimTask() (*domain.Task, error) {
	return taskRepo{s}.Claim()
}

// GetTasksByRun returns all tasks for a run.
func (s *Store) GetTasksByRun(runID uuid.UUID) ([]*domain.Task, error) {
	return taskRepo{s}.ByRun(runID)
}

// GetTasksByStep returns all tasks for a step.
func (s *Store) GetTasksByStep(stepID uuid.UUID) ([]*domain.Task, error) {
	return taskRepo{s}.ByStep(stepID)
}

// ClaimedCount returns the number of claimed tasks for a run.
func (s *Store) ClaimedCount(runID uuid.UUID) (int, error) {
	return taskRepo{s}.ClaimedCount(runID)
}

// PendingCount returns the number of pending tasks for a run.
func (s *Store) PendingCount(runID uuid.UUID) (int, error) {
	return taskRepo{s}.PendingCount(runID)
}

// ─── Artifact CRUD ────────────────────────────────────────────────────────────

// CreateArtifact inserts an artifact.
func (s *Store) CreateArtifact(a *Artifact) error {
	return artifactRepo{s}.Create(a)
}

// GetArtifact fetches an artifact by ID.
func (s *Store) GetArtifact(id uuid.UUID) (*Artifact, error) {
	return artifactRepo{s}.Get(id)
}

// GetArtifactsByRun returns all artifacts for a run.
func (s *Store) GetArtifactsByRun(runID uuid.UUID) ([]*Artifact, error) {
	return artifactRepo{s}.ByRun(runID)
}

// ─── Task Business Operations ─────────────────────────────────────────────────

// UpdateTaskStatus updates a task's status without version checking (lightweight,
// used by workers for fire-and-forget status updates). Sets claimed_at/completed_at
// timestamps automatically based on target status.
// TODO: implement using taskRepo{}.UpdateStatus
func (s *Store) UpdateTaskStatus(id uuid.UUID, status domain.TaskStatus) error {
	return taskRepo{s}.UpdateStatus(id, status)
}

// SetTaskResult writes the agent result back to a task after completion.
// Sets status to completed, agent_result, result_summary, and completed_at.
// TODO: implement using taskRepo{}.SetResult
func (s *Store) SetTaskResult(id uuid.UUID, result json.RawMessage, summary string) error {
	return taskRepo{s}.SetResult(id, result, summary, domain.TaskStatusCompleted)
}

// SetTaskFailure writes a failed result back to a task atomically.
// Sets status to failed, agent_result, result_summary, and completed_at.
func (s *Store) SetTaskFailure(id uuid.UUID, result json.RawMessage, summary string) error {
	return taskRepo{s}.SetResult(id, result, summary, domain.TaskStatusFailed)
}

// ─── Run Status Update ─────────────────────────────────────────────────────────

// UpdateRunStatus atomically transitions a run's status from `from` to `to`
// only if current status matches `from` and version matches (optimistic lock).
// TODO: implement using runRepo{}.UpdateStatus
func (s *Store) UpdateRunStatus(id uuid.UUID, from, to domain.RunStatus, version int) error {
	return runRepo{s}.UpdateStatus(id, from, to, version)
}

// ─── Step Status Update ────────────────────────────────────────────────────────

// UpdateStepStatus performs a lightweight single-field status update on a step.
// Does NOT use optimistic locking — use UpdateStep for full updates.
// TODO: implement using stepRepo{}.UpdateStatus
func (s *Store) UpdateStepStatus(id uuid.UUID, to domain.StepStatus) error {
	return stepRepo{s}.UpdateStatus(id, to)
}

// ─── LoopIteration CRUD ──────────────────────────────────────────────────────

// CreateLoopIteration inserts a loop iteration record.
func (s *Store) CreateLoopIteration(li *domain.LoopIteration) error {
	return loopIterationRepo{s}.Create(li)
}

// UpdateLoopIteration updates a loop iteration.
func (s *Store) UpdateLoopIteration(li *domain.LoopIteration) error {
	return loopIterationRepo{s}.Update(li)
}

// GetLoopIteration fetches a specific iteration.
func (s *Store) GetLoopIteration(runID, stepID uuid.UUID, iteration int) (*domain.LoopIteration, error) {
	return loopIterationRepo{s}.Get(runID, stepID, iteration)
}

// GetLoopIterationsByStep returns all iterations for a loop step.
func (s *Store) GetLoopIterationsByStep(runID, stepID uuid.UUID) ([]*domain.LoopIteration, error) {
	return loopIterationRepo{s}.ByStep(runID, stepID)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// Tx wraps a transaction with helper commit/rollback.
type Tx struct {
	Tx *sql.Tx
}

// Commit commits the transaction.
func (tx *Tx) Commit() error {
	return tx.Tx.Commit()
}

// Rollback aborts the transaction.
func (tx *Tx) Rollback() error {
	return tx.Tx.Rollback()
}

// WithTx runs fn within a transaction, committing on success, rolling back on panic.
func (s *Store) WithTx(fn func(*Tx) error) error {
	tx, err := s.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()
	if err := fn(&Tx{Tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Now returns current time for store use.
func Now() time.Time { return time.Now().UTC() }
