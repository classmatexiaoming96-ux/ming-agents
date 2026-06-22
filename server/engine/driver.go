package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
)

// RunDriver drives end-to-end execution of a compiled run.
// Epic 4.2: 引擎↔队列对接 — Step→Task 落队列，调度结果回流.
// Epic 2.8: Run 状态持久化与恢复 — 崩溃后从 checkpoint 续跑.
// Epic 2.12: RunRecord stores resolved params for replay.
// Epic 2.13: DegradationReporter captures parameter fallbacks for alerting.
// Epic 2.14: CriticallyReporter validates run completeness.
type RunDriver struct {
	store            *store.Store
	registry         *adapter.Registry
	engine           *Engine
	recordStore      RunRecordStore
	degradationStore DegradationStore // Epic 2.13
	mu               sync.RWMutex
	runners          map[uuid.UUID]*runExecution
}

// Runner drives a single run instance.
type Runner interface {
	Launch(runID uuid.UUID) error
	ResumeRun(runID uuid.UUID) (*RecoveryResult, error)
	OnTaskCompleted(taskID uuid.UUID) error
	EnqueueTasks(tasks []*domain.Task) error
}

type runExecution struct {
	driver             *RunDriver
	runID              uuid.UUID
	store              *store.Store
	registry           *adapter.Registry
	translator         *Translator
	scheduler          *Scheduler
	ctx                *Context
	pm                 *PersistenceManager
	engine             *Engine
	completed          map[string]bool // stepName → completed
	mu                 sync.Mutex
	recordStore        RunRecordStore
	degradationStore   DegradationStore // Epic 2.13
	recorder           *RunRecorder
	criticallyReporter *CriticallyReporter // Epic 2.14
}

// NewRunDriver creates a new run driver.
func NewRunDriver(s *store.Store, r *adapter.Registry, e *Engine) *RunDriver {
	return &RunDriver{
		store:    s,
		registry: r,
		engine:   e,
		runners:  make(map[uuid.UUID]*runExecution),
	}
}

// SetRecordStore sets the record store for capturing run decisions.
func (d *RunDriver) SetRecordStore(rs RunRecordStore) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recordStore = rs
}

// SetDegradationStore sets the degradation store for capturing parameter fallbacks.
// Epic 2.13
func (d *RunDriver) SetDegradationStore(ds DegradationStore) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.degradationStore = ds
}

func (d *RunDriver) newRunner(runID uuid.UUID) *runExecution {
	return &runExecution{
		driver:             d,
		runID:              runID,
		store:              d.store,
		registry:           d.registry,
		translator:         NewTranslator(d.store, d.registry),
		ctx:                NewContext(),
		pm:                 NewPersistenceManager(d.store),
		engine:             d.engine,
		completed:          make(map[string]bool),
		recordStore:        d.recordStore,
		degradationStore:   d.degradationStore,
		criticallyReporter: NewCriticallyReporter(),
	}
}

func (d *RunDriver) createRunner(runID uuid.UUID) (*runExecution, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.runners[runID]; ok {
		return nil, fmt.Errorf("runner already exists for run %s", runID)
	}
	runner := d.newRunner(runID)
	d.runners[runID] = runner
	return runner, nil
}

func (d *RunDriver) removeRunner(runID uuid.UUID, runner *runExecution) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.runners[runID] == runner {
		delete(d.runners, runID)
	}
}

// Launch kicks off execution of a run asynchronously.
// Tasks are dispatched to agent_task_queue.
func (d *RunDriver) Launch(runID uuid.UUID) error {
	runner, err := d.createRunner(runID)
	if err != nil {
		return err
	}
	if err := runner.Launch(runID); err != nil {
		d.removeRunner(runID, runner)
		return err
	}
	return nil
}

// OnTaskCompleted routes task completion callbacks to the runner that owns the task's run.
func (d *RunDriver) OnTaskCompleted(taskID uuid.UUID) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	task, err := d.store.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}
	runner := d.runners[task.RunID]
	if runner == nil {
		return fmt.Errorf("runner not found for run %s", task.RunID)
	}
	return runner.OnTaskCompleted(taskID)
}

// ResumeRun recovers and resumes a crashed run.
func (d *RunDriver) ResumeRun(runID uuid.UUID) (*RecoveryResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.runners[runID]; ok {
		return nil, fmt.Errorf("runner already exists for run %s", runID)
	}
	runner := d.newRunner(runID)
	d.runners[runID] = runner
	result, err := runner.ResumeRun(runID)
	if err != nil {
		if d.runners[runID] == runner {
			delete(d.runners, runID)
		}
	}
	return result, err
}

// EnqueueTasks enqueues tasks to the store.
func (d *RunDriver) EnqueueTasks(tasks []*domain.Task) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.store.CreateTasks(tasks); err != nil {
		return fmt.Errorf("enqueue task: %w", err)
	}
	return nil
}

func (d *runExecution) Launch(runID uuid.UUID) error {
	run, err := d.store.GetRun(runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if !run.CanStart() {
		return fmt.Errorf("run %s cannot start (status=%s)", run.ID, run.Status)
	}

	run.Status = domain.RunStatusRunning
	if err := d.store.UpdateRun(run); err != nil {
		return fmt.Errorf("start run: %w", err)
	}

	steps, err := d.store.GetStepsByRun(runID)
	if err != nil {
		return fmt.Errorf("get steps: %w", err)
	}

	// Build scheduler for this run.
	scheduler, err := d.engine.SchedulerForRun(run, steps)
	if err != nil {
		return fmt.Errorf("build scheduler: %w", err)
	}
	d.scheduler = scheduler

	// Initialize recorder if record store is configured (Epic 2.12).
	if d.recordStore != nil {
		d.recorder = NewRunRecorder(d.recordStore, runID, run.Name, len(steps), d.degradationStore)
	}

	// Recover from checkpoint if exists (Epic 2.8).
	if recovered, err := d.scheduler.RecoverState(runID); err != nil {
		log.Printf("[driver] checkpoint recover: %v", err)
	} else if recovered {
		log.Printf("[driver] recovered scheduler state for run %s", runID)
		// Restore completed/skipped state into driver.
		for stepName := range d.scheduler.GetCompletedSteps() {
			d.completeStep(stepName, nil)
		}
	}

	go d.dispatchLoop(run, steps)
	return nil
}

func (d *runExecution) dispatchLoop(run *domain.Run, allSteps []*domain.Step) {
	stepMap := make(map[string]*domain.Step)
	for _, s := range allSteps {
		stepMap[s.Name] = s
	}

	for {
		if d.isRunComplete(run.ID) {
			d.finalizeRun(run, allSteps)
			d.removeFromDriver()
			return
		}

		claimed, _ := d.store.ClaimedCount(run.ID)
		pending, _ := d.store.PendingCount(run.ID)
		slots := d.scheduler.PendingSlots(claimed, pending)
		if slots <= 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		d.mu.Lock()
		readyNodes := d.scheduler.Advance(d.ctx, d.completedSnapshotLocked())
		d.mu.Unlock()

		for _, node := range readyNodes {
			if slots <= 0 {
				break
			}
			step := stepMap[node.ID]
			if step == nil {
				continue
			}

			// Check if the node has a when condition that evaluates to false.
			if node.When != nil && *node.When != "" {
				ok, err := d.ctx.EvaluateCondition(*node.When)
				if err != nil {
					log.Printf("[driver] evaluate when condition %s for step %s: %v", *node.When, step.Name, err)
					continue
				}
				if !ok {
					// Skip this step.
					step.Status = domain.StepStatusSkipped
					step.SkipReasonStr = fmt.Sprintf("when expression %q evaluated to false", *node.When)
					_ = d.store.UpdateStep(step)

					d.mu.Lock()
					d.scheduler.SkipStep(step.Name)
					if d.recorder != nil {
						d.recorder.RecordSkippedStep(step.Name, step.SkipReasonStr)
					}
					d.mu.Unlock()
					d.completeStep(step.Name, nil)
					continue
				}
			}

			tasks, err := d.translator.TranslateStep(step, d.ctx)
			if err != nil {
				log.Printf("[driver] translate step %s: %v", step.Name, err)
				continue
			}
			// Record resolved params (Epic 2.12).
			if d.recorder != nil && tasks != nil {
				resolvedInputs, _ := resolveInputsForRecord(step, d.ctx)
				d.recorder.RecordResolvedParams(step.Name, resolvedInputs)
			}
			if tasks == nil {
				d.completeStep(step.Name, nil)
				continue
			}

			if err := d.store.CreateTasks(tasks); err != nil {
				log.Printf("[driver] create tasks: %v", err)
				step.Status = domain.StepStatusFailed
				step.SkipReasonStr = fmt.Sprintf("enqueue task: %v", err)
				_ = d.store.UpdateStep(step)
				d.completeStep(step.Name, nil)
				continue
			}
			slots -= len(tasks)

			step.Status = domain.StepStatusRunning
			_ = d.store.UpdateStep(step)
		}
	}
}

func (d *runExecution) removeFromDriver() {
	if d.driver != nil {
		d.driver.removeRunner(d.runID, d)
	}
}

// OnTaskCompleted is called when a task completes (by the worker callback).
func (d *runExecution) OnTaskCompleted(taskID uuid.UUID) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	task, err := d.store.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}
	step, err := d.store.GetStep(task.StepID)
	if err != nil {
		return fmt.Errorf("get step: %w", err)
	}
	if step.Status == domain.StepStatusCompleted || step.Status == domain.StepStatusFailed || step.Status == domain.StepStatusSkipped {
		return nil
	}

	// Persist step output if all tasks done.
	tasks, _ := d.store.GetTasksByStep(step.ID)
	allDone := true
	anyFailed := false
	for _, t := range tasks {
		if t.Status != domain.TaskStatusCompleted && t.Status != domain.TaskStatusFailed {
			allDone = false
		}
		if t.Status == domain.TaskStatusFailed {
			anyFailed = true
		}
	}

	if allDone {
		if anyFailed {
			step.Status = domain.StepStatusFailed
		} else {
			step.Status = domain.StepStatusCompleted
		}

		outputs := aggregateTaskOutputs(step, tasks)
		if err := d.pm.PersistStepOutput(step, outputs); err != nil {
			log.Printf("[driver] persist step output: %v", err)
			_ = d.store.UpdateStep(step)
		}
		for k, v := range outputs {
			d.ctx.SetOutput(step.Name, k, v)
		}

		d.completeStepLocked(step.Name, outputs)

		// Persist scheduler state checkpoint (Epic 2.8).
		if err := d.scheduler.PersistState(step.RunID); err != nil {
			log.Printf("[driver] persist state: %v", err)
		}

		// Also persist DB-level snapshot for full recovery.
		runPtr, _ := d.store.GetRun(step.RunID)
		allTasks, _ := d.store.GetTasksByRun(step.RunID)
		allSteps, _ := d.store.GetStepsByRun(step.RunID)
		_ = d.pm.Snapshot(runPtr, allSteps, allTasks)
	}

	return nil
}

func (d *runExecution) markCompletedLocked(stepName string) {
	d.completed[stepName] = true
}

func (d *runExecution) completeStep(stepName string, outputs map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.completeStepLocked(stepName, outputs)
}

func (d *runExecution) completeStepLocked(stepName string, outputs map[string]any) {
	d.markCompletedLocked(stepName)
	d.scheduler.StepCompleted(stepName)
	if outputs != nil {
		d.scheduler.MarkStepCompleted(stepName, outputs)
	}
}

func (d *runExecution) completedSnapshotLocked() map[string]bool {
	out := make(map[string]bool, len(d.completed))
	for k, v := range d.completed {
		out[k] = v
	}
	return out
}

func aggregateTaskOutputs(step *domain.Step, tasks []*domain.Task) map[string]any {
	if len(tasks) == 1 {
		return taskOutputMap(step, tasks[0])
	}
	byIndex := make(map[int]map[string]any, len(tasks))
	byKey := make(map[string][]any)
	for _, task := range tasks {
		out := taskOutputMap(step, task)
		entry := make(map[string]any, len(out)+2)
		entry["_task_id"] = task.ID.String()
		entry["_index"] = task.Iteration
		for k, v := range out {
			entry[k] = v
		}
		byIndex[task.Iteration] = entry
	}
	indexes := make([]int, 0, len(byIndex))
	for idx := range byIndex {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	results := make([]map[string]any, 0, len(indexes))
	for _, idx := range indexes {
		entry := byIndex[idx]
		for k, v := range entry {
			if k == "_task_id" || k == "_index" {
				continue
			}
			byKey[k] = append(byKey[k], v)
		}
		results = append(results, entry)
	}
	aggregated := map[string]any{"results": results}
	for k, v := range byKey {
		aggregated[k] = v
	}
	return aggregated
}

func taskOutputMap(step *domain.Step, task *domain.Task) map[string]any {
	var result adapter.AgentResult
	if len(task.AgentResult) > 0 {
		if err := json.Unmarshal(task.AgentResult, &result); err != nil {
			result.Output = string(task.AgentResult)
		}
	}
	return (&ContextPropagator{}).extractOutputs(step, &result, task.ResultSummary.String)
}

func (d *runExecution) isRunComplete(runID uuid.UUID) bool {
	steps, err := d.store.GetStepsByRun(runID)
	if err != nil {
		return false
	}
	for _, s := range steps {
		if s.Status == domain.StepStatusPending || s.Status == domain.StepStatusRunning {
			return false
		}
	}
	return true
}

func (d *runExecution) finalizeRun(run *domain.Run, steps []*domain.Step) {
	anyFailed := false
	for _, s := range steps {
		if s.Status == domain.StepStatusFailed {
			anyFailed = true
		}
	}
	if anyFailed {
		run.Status = domain.RunStatusFailed
	} else {
		run.Status = domain.RunStatusCompleted
	}
	run.EndedAt.Valid = true
	run.EndedAt.Time = store.Now()
	_ = d.store.UpdateRun(run)

	// Evaluate critically nodes and save run record (Epic 2.12, 2.14).
	if d.recorder != nil {
		var criticallyResults []CriticallyResult
		if run.CriticallyNodesStr != "" {
			var nodes []CriticallyNode
			if err := json.Unmarshal([]byte(run.CriticallyNodesStr), &nodes); err == nil && len(nodes) > 0 {
				// Build a RunRecord from the recorder's current state for evaluation.
				// We need to pass the RunRecord data to EvaluateAll.
				rec := RunRecord{
					RunID:               run.ID,
					TemplateName:        run.TemplateName,
					Timestamp:           time.Now().UTC(),
					ResolvedParams:      d.recorder.GetResolvedParams(),
					EvaluatedAssertions: d.recorder.GetAssertions(),
					EffectiveThresholds: d.recorder.GetThresholds(),
					SkippedSteps:        d.recorder.GetSkippedSteps(),
					RunStatus:           string(run.Status),
					TotalSteps:          len(steps),
				}
				criticallyResults = d.criticallyReporter.EvaluateAll(nodes, rec)
			}
		}
		_ = d.recorder.Save(string(run.Status), criticallyResults)
	}
}

// ResumeRun recovers and resumes a crashed run.
func (d *runExecution) ResumeRun(runID uuid.UUID) (*RecoveryResult, error) {
	result, err := d.pm.RecoverRun(runID)
	if err != nil {
		return nil, fmt.Errorf("recover run: %w", err)
	}

	if result.Run == nil {
		return result, fmt.Errorf("run %s not found", runID)
	}

	// Build scheduler for this run.
	scheduler, err := d.engine.SchedulerForRun(result.Run, result.Steps)
	if err != nil {
		return nil, fmt.Errorf("build scheduler: %w", err)
	}
	d.scheduler = scheduler

	// Recover scheduler state from checkpoint (Epic 2.8).
	if recovered, err := d.scheduler.RecoverState(runID); err != nil {
		log.Printf("[driver] checkpoint recover: %v", err)
	} else if recovered {
		log.Printf("[driver] recovered scheduler state for run %s", runID)
		// Restore completed/skipped state into driver.
		for stepName := range d.scheduler.GetCompletedSteps() {
			d.mu.Lock()
			d.markCompletedLocked(stepName)
			d.mu.Unlock()
		}
	}

	// Restore context.
	if result.Context != nil {
		d.ctx = result.Context
	}

	// Re-enqueue in-flight tasks as pending.
	for _, task := range result.InFlightTasks {
		task.Status = domain.TaskStatusPending
		_ = d.store.UpdateTask(task)
	}

	// Resume run.
	result.Run.Status = domain.RunStatusRunning
	_ = d.store.UpdateRun(result.Run)

	go d.dispatchLoop(result.Run, result.Steps)

	return result, nil
}

// EnqueueTasks enqueues tasks to the store.
func (d *runExecution) EnqueueTasks(tasks []*domain.Task) error {
	if err := d.store.CreateTasks(tasks); err != nil {
		return fmt.Errorf("enqueue task: %w", err)
	}
	return nil
}

// RecoverResult is an alias for RecoveryResult exported from persistence.
type RecoverResult = RecoveryResult

func runFromID(store *store.Store, runID uuid.UUID) (*domain.Run, error) {
	return store.GetRun(runID)
}
