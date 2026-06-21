package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
)

// PersistenceManager handles Run state persistence and crash recovery.
// Epic 2.8: Run 状态持久化与恢复 — 崩溃后从 DB 持久化状态续跑.
type PersistenceManager struct {
	store *store.Store
}

// NewPersistenceManager creates a new persistence manager.
func NewPersistenceManager(s *store.Store) *PersistenceManager {
	return &PersistenceManager{store: s}
}

// Snapshot captures the current state of a run into a JSON artifact.
// This is called periodically or after each step completion.
func (pm *PersistenceManager) Snapshot(run *domain.Run, steps []*domain.Step, tasks []*domain.Task) error {
	type snapshot struct {
		RunID     uuid.UUID                 `json:"run_id"`
		RunStatus domain.RunStatus          `json:"run_status"`
		Steps     []stepSnapshot            `json:"steps"`
		Tasks     []taskSnapshot            `json:"tasks"`
		Ctx       map[string]map[string]any `json:"context"`
	}
	ss := snapshot{
		RunID:     run.ID,
		RunStatus: run.Status,
		Ctx:       make(map[string]map[string]any),
	}
	for _, st := range steps {
		ss.Steps = append(ss.Steps, stepSnapshot{
			ID:        st.ID,
			Name:      st.Name,
			Status:    st.Status,
			Iteration: st.Iteration,
			Attempt:   st.Attempt,
		})
		if outputs, ok := runtimeOutputsForStep(st, tasks); ok {
			ss.Ctx[st.Name] = outputs
		}
	}
	for _, t := range tasks {
		ss.Tasks = append(ss.Tasks, taskSnapshot{
			ID:      t.ID,
			StepID:  t.StepID,
			Status:  t.Status,
			Claimed: t.ClaimedAt.Valid,
		})
	}
	raw, err := json.Marshal(ss)
	if err != nil {
		return fmt.Errorf("serialize snapshot: %w", err)
	}
	// Persist as an artifact.
	a := &store.Artifact{
		ID:      uuid.New(),
		RunID:   run.ID,
		StepID:  uuid.Nil,
		Name:    "_run_snapshot",
		Type:    "json",
		Content: string(raw),
	}
	return pm.store.CreateArtifact(a)
}

// RecoverRun reconstructs execution state from the DB.
// It finds the most recent snapshot for a run and restores:
// - Run status
// - Step statuses
// - In-progress tasks (claimed but not completed)
// - Context outputs from completed steps.
func (pm *PersistenceManager) RecoverRun(runID uuid.UUID) (*RecoveryResult, error) {
	run, err := pm.store.GetRun(runID)
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}

	steps, err := pm.store.GetStepsByRun(runID)
	if err != nil {
		return nil, fmt.Errorf("get steps: %w", err)
	}

	tasks, err := pm.store.GetTasksByRun(runID)
	if err != nil {
		return nil, fmt.Errorf("get tasks: %w", err)
	}

	artifacts, err := pm.store.GetArtifactsByRun(runID)
	if err != nil {
		return nil, fmt.Errorf("get artifacts: %w", err)
	}

	// Find the most recent snapshot.
	var latestSnapshot *store.Artifact
	for _, a := range artifacts {
		if a.Name == "_run_snapshot" {
			if latestSnapshot == nil || a.CreatedAt.After(latestSnapshot.CreatedAt) {
				latestSnapshot = a
			}
		}
	}

	result := &RecoveryResult{
		Run:       run,
		Steps:     steps,
		Tasks:     tasks,
		Artifacts: artifacts,
		Recovered: true,
	}

	if latestSnapshot != nil {
		var snap snapshot
		if err := json.Unmarshal([]byte(latestSnapshot.Content), &snap); err != nil {
			log.Printf("WARN: failed to parse snapshot: %v", err)
		} else {
			result.Snapshot = &snap
		}
	}

	// Restore context from completed steps.
	ctx := NewContext()
	restoredSteps := make(map[string]bool)
	for _, st := range steps {
		if outputs, ok := runtimeOutputsForStep(st, tasks); ok {
			for k, v := range outputs {
				ctx.SetOutput(st.Name, k, v)
			}
			restoredSteps[st.Name] = true
		}
	}
	if result.Snapshot != nil {
		for stepName, outputs := range result.Snapshot.Ctx {
			if restoredSteps[stepName] {
				continue
			}
			for k, v := range outputs {
				ctx.SetOutput(stepName, k, v)
			}
		}
	}
	result.Context = ctx

	// Identify in-flight tasks (claimed but not completed).
	var inFlight []*domain.Task
	for _, t := range tasks {
		if t.Status == domain.TaskStatusClaimed {
			inFlight = append(inFlight, t)
		}
	}
	result.InFlightTasks = inFlight

	// Identify steps that need to be resumed.
	var pendingSteps []*domain.Step
	for _, st := range steps {
		if st.Status == domain.StepStatusPending || st.Status == domain.StepStatusRunning {
			pendingSteps = append(pendingSteps, st)
		}
	}
	result.PendingSteps = pendingSteps

	return result, nil
}

func runtimeOutputsForStep(step *domain.Step, tasks []*domain.Task) (map[string]any, bool) {
	if step.Status != domain.StepStatusCompleted {
		return nil, false
	}
	var stepTasks []*domain.Task
	for _, task := range tasks {
		if task.StepID == step.ID && task.Status == domain.TaskStatusCompleted {
			stepTasks = append(stepTasks, task)
		}
	}
	if len(stepTasks) == 0 {
		return nil, false
	}
	return aggregateTaskOutputs(step, stepTasks), true
}

// RecoveryResult holds the result of recovering a run.
type RecoveryResult struct {
	Run           *domain.Run
	Steps         []*domain.Step
	Tasks         []*domain.Task
	Artifacts     []*store.Artifact
	InFlightTasks []*domain.Task // tasks that were claimed when crash occurred
	PendingSteps  []*domain.Step // steps not yet completed
	Snapshot      *snapshot      `json:"snapshot,omitempty"`
	Context       *Context       // restored context from completed steps
	Recovered     bool
}

// PersistStepOutput saves step outputs to the DB and updates the step record.
func (pm *PersistenceManager) PersistStepOutput(step *domain.Step, outputs map[string]any) error {
	raw, err := json.Marshal(outputs)
	if err != nil {
		return fmt.Errorf("marshal outputs: %w", err)
	}
	step.OutputsJSON = sql.NullString{String: string(raw), Valid: true}
	return pm.store.UpdateStep(step)
}

// CheckpointRun persists the current run state (a lightweight snapshot).
// Called after each step completion.
func (pm *PersistenceManager) CheckpointRun(run *domain.Run) error {
	run.UpdatedAt = store.Now()
	return pm.store.UpdateRun(run)
}

type stepSnapshot struct {
	ID        uuid.UUID         `json:"id"`
	Name      string            `json:"name"`
	Status    domain.StepStatus `json:"status"`
	Iteration int               `json:"iteration"`
	Attempt   int               `json:"attempt"`
}

type taskSnapshot struct {
	ID      uuid.UUID         `json:"id"`
	StepID  uuid.UUID         `json:"step_id"`
	Status  domain.TaskStatus `json:"status"`
	Claimed bool              `json:"claimed"`
}

type snapshot struct {
	RunID     uuid.UUID                 `json:"run_id"`
	RunStatus domain.RunStatus          `json:"run_status"`
	Steps     []stepSnapshot            `json:"steps"`
	Tasks     []taskSnapshot            `json:"tasks"`
	Ctx       map[string]map[string]any `json:"context"`
}
