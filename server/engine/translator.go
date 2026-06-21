package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/google/uuid"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
	"github.com/ming-agents/server/workflow"
)

// Translator translates WDL steps into agent tasks for the queue.
// Epic 2.4: Dynamic fan-out — number of tasks determined by upstream output.
type Translator struct {
	store    *store.Store
	registry *adapter.Registry
}

// NewTranslator creates a new translator.
func NewTranslator(s *store.Store, r *adapter.Registry) *Translator {
	return &Translator{store: s, registry: r}
}

// TranslateStep translates a single step into one or more tasks.
// For a "task" step: creates one task.
// For a "loop" step: creates one task per loop iteration (driven externally).
// For a "conditional" step: evaluates condition; if false, step is skipped.
// Fan-out: if inputs contain a list reference (e.g. "${upstream.items}"),
// this creates one task per list element.
func (t *Translator) TranslateStep(step *domain.Step, ctx *Context) ([]*domain.Task, error) {
	if err := hydrateStepInputs(step); err != nil {
		return nil, err
	}
	// Resolve inputs from context.
	resolvedInputs, err := t.resolveInputs(step, ctx)
	if err != nil {
		return nil, err
	}

	// Check for conditional skip.
	if step.StepType == domain.StepTypeConditional {
		skip, reason, err := t.evaluateCondition(step, resolvedInputs, ctx)
		if err != nil {
			return nil, err
		}
		if skip {
			step.Status = domain.StepStatusSkipped
			step.SkipReasonStr = reason
			if t.store != nil {
				_ = t.store.UpdateStep(step)
			}
			return nil, nil // No tasks generated; step is skipped.
		}
	}

	// Check for list fan-out.
	listItems, err := t.extractList(resolvedInputs)
	if err != nil {
		return nil, err
	}
	if len(listItems) > 0 {
		return t.fanOut(step, listItems, ctx)
	}

	// Single task.
	return []*domain.Task{t.createTask(step, resolvedInputs, ctx)}, nil
}

// resolveInputs resolves step inputs using the execution context.
func (t *Translator) resolveInputs(step *domain.Step, ctx *Context) (map[string]any, error) {
	if err := hydrateStepInputs(step); err != nil {
		return nil, err
	}
	inputs := copyMap(step.InputsMap)
	// Resolve each input by replacing ${ref} with context values.
	for k, v := range inputs {
		if s, ok := v.(string); ok {
			inputs[k] = ctx.RenderTemplate(s)
		}
	}
	return inputs, nil
}

// evaluateCondition evaluates a conditional step's `when` expression.
// Returns (skip=true, reason) if the condition is false.
func (t *Translator) evaluateCondition(step *domain.Step, inputs map[string]any, ctx *Context) (bool, string, error) {
	// The "when" field is stored in the step's inputs JSON as a "_when" key.
	when, ok := inputs["_when"].(string)
	if !ok {
		return false, "", nil
	}
	ok, err := ctx.EvaluateCondition(when)
	if err != nil {
		return false, "", fmt.Errorf("evaluate condition %q: %w", when, err)
	}
	// skip=true means condition is FALSE (skip the step).
	return !ok, fmt.Sprintf("condition %q evaluated to false", when), nil
}

// extractList checks if inputs contain a list reference and returns its items.
// It handles both direct []any and JSON string representations of lists.
func (t *Translator) extractList(inputs map[string]any) ([]any, error) {
	if arr, ok := inputListValue(inputs["_list"]); ok {
		return arr, nil
	}
	if arr, ok := inputListValue(inputs["_items"]); ok {
		return arr, nil
	}

	var foundKey string
	var found []any
	for k, v := range inputs {
		if k == "_list" || k == "_items" {
			continue
		}
		arr, ok := inputListValue(v)
		if !ok {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("ambiguous fan-out inputs %q and %q; use _list or _items to select the source", foundKey, k)
		}
		foundKey = k
		found = arr
	}
	return found, nil
}

func inputListValue(v any) ([]any, bool) {
	if arr, ok := v.([]any); ok {
		return arr, true
	}
	// Handle JSON string that represents a list.
	if s, ok := v.(string); ok {
		if len(s) > 0 && s[0] == '[' {
			var arr []any
			if err := json.Unmarshal([]byte(s), &arr); err == nil {
				return arr, true
			}
		}
	}
	return nil, false
}

// fanOut creates one task per item in the list.
func (t *Translator) fanOut(step *domain.Step, items []any, ctx *Context) ([]*domain.Task, error) {
	if err := hydrateStepInputs(step); err != nil {
		return nil, err
	}
	var tasks []*domain.Task
	for i, item := range items {
		itemInputs := map[string]any{
			"_item":  item,
			"_index": i,
			"_total": len(items),
		}
		// Copy non-special inputs.
		for k, v := range step.InputsMap {
			if k != "_list" && k != "_items" && k != "_adapter" {
				itemInputs[k] = v
			}
		}
		for k, v := range itemInputs {
			if s, ok := v.(string); ok {
				itemInputs[k] = renderTaskTemplate(s, ctx, itemInputs)
			}
		}
		task := t.createTaskWithInputs(step, itemInputs, ctx)
		task.Iteration = i
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// createTask creates a single task for a step.
func (t *Translator) createTask(step *domain.Step, inputs map[string]any, ctx *Context) *domain.Task {
	return t.createTaskWithInputs(step, inputs, ctx)
}

// createTaskWithInputs creates a task with given inputs.
func (t *Translator) createTaskWithInputs(step *domain.Step, inputs map[string]any, ctx *Context) *domain.Task {
	// Build agent request JSON.
	reqJSON := buildAgentRequest(step, inputs)
	return &domain.Task{
		ID:           uuid.New(),
		RunID:        step.RunID,
		StepID:       step.ID,
		Iteration:    step.Iteration,
		Attempt:      step.Attempt,
		Status:       domain.TaskStatusPending,
		AdapterKey:   adapterKeyForStep(step),
		AgentRequest: reqJSON,
	}
}

// buildAgentRequest builds an agent request from step + inputs.
func buildAgentRequest(step *domain.Step, inputs map[string]any) json.RawMessage {
	req := adapter.AgentRequest{}
	// If there's a "prompt" in inputs, use it.
	if prompt, ok := inputs["prompt"].(string); ok {
		req.Prompt = prompt
	} else {
		// Serialize inputs as raw JSON.
		req.RawJSON, _ = json.Marshal(inputs)
	}
	raw, _ := json.Marshal(req)
	return raw
}

// adapterKeyForStep returns the adapter key for a step.
func adapterKeyForStep(step *domain.Step) string {
	if step.AdapterKey != "" {
		return step.AdapterKey
	}
	if err := hydrateStepInputs(step); err == nil {
		if v, ok := step.InputsMap["_adapter"].(string); ok && v != "" {
			return v
		}
		if v, ok := step.InputsMap["adapter"].(string); ok && v != "" {
			return v
		}
	}
	return "fake"
}

func hydrateStepInputs(step *domain.Step) error {
	if step.InputsMap == nil {
		step.InputsMap = make(map[string]any)
	}
	if step.InputsJSON.Valid && len(step.InputsMap) == 0 {
		if err := json.Unmarshal([]byte(step.InputsJSON.String), &step.InputsMap); err != nil {
			return fmt.Errorf("unmarshal inputs: %w", err)
		}
	}
	if step.AdapterKey == "" {
		if v, ok := step.InputsMap["_adapter"].(string); ok {
			step.AdapterKey = v
		}
	}
	return nil
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func renderTaskTemplate(s string, ctx *Context, locals map[string]any) string {
	rendered := ctx.RenderTemplate(s)
	re := regexp.MustCompile(`\$\{([^}]+)\}`)
	return re.ReplaceAllStringFunc(rendered, func(match string) string {
		key := match[2 : len(match)-1]
		v, ok := locals[key]
		if !ok {
			return match
		}
		if vs, ok := v.(string); ok {
			return vs
		}
		if bs, err := json.Marshal(v); err == nil {
			return string(bs)
		}
		return fmt.Sprintf("%v", v)
	})
}

// ─── Scheduler ────────────────────────────────────────────────────────────────
// Epic 2.5: Dependency solving /调度 — topological sort + ready-set + parallel degree control.
// Supports runtime addition of new Tasks entering the ready-set.

// Scheduler drives the DAG execution, respecting dependencies and max parallelism.
type Scheduler struct {
	store                *store.Store
	dag                  *workflow.DAG
	maxParallel          int
	readySet             map[string]*workflow.Node // nodes with all inputs satisfied
	pendingCount         int
	blocked              map[string]bool           // nodes blocked by unmet dependencies
	skipped              map[string]bool           // nodes skipped due to when condition or parent skip
	completedSteps       map[string]bool           // steps completed (for checkpointing)
	stepOutputs          map[string]map[string]any // step outputs for recovery
	wasAdvanced          bool                      // tracks whether Advance was ever called (for first-call optimization)
	returned             map[string]bool           // nodes already returned by Advance (prevents re-add to readySet)
	processedCompletions map[string]bool           // steps whose edges have been released (prevents double-decrement)
}

// NewScheduler creates a new scheduler for a run.
// maxParallel controls how many tasks can run concurrently.
func NewScheduler(s *store.Store, dag *workflow.DAG, maxParallel int) *Scheduler {
	return &Scheduler{
		store:                s,
		dag:                  dag,
		maxParallel:          maxParallel,
		readySet:             make(map[string]*workflow.Node),
		blocked:              make(map[string]bool),
		skipped:              make(map[string]bool),
		completedSteps:       make(map[string]bool),
		stepOutputs:          make(map[string]map[string]any),
		returned:             make(map[string]bool),
		processedCompletions: make(map[string]bool),
	}
}

// InitReadySet computes the initial ready set using Kahn's algorithm.
// All nodes with in-degree 0 enter the ready set.
func (s *Scheduler) InitReadySet() {
	// Kahn's algorithm: start with all in-degree 0 nodes.
	for _, n := range s.dag.Nodes() {
		if s.dag.InDegree(n.ID) == 0 {
			s.readySet[n.ID] = n
		}
	}
}

// Advance evaluates which steps are ready to run using Kahn's algorithm.
// It maintains in-degree tracking internally, decrementing when parents complete.
// After a step completes, call StepCompleted to unblock dependents.
// It also propagates skips: when a parent is skipped, children become skipped too.
func (s *Scheduler) Advance(ctx *Context, completedSteps map[string]bool) []*workflow.Node {
	var newlyReady []*workflow.Node

	// First call optimization: return all nodes in readySet only on the actual first call
	// (when Advance has never been called AND there are no completedSteps or skips yet)
	if !s.wasAdvanced && len(completedSteps) == 0 && len(s.skipped) == 0 {
		s.wasAdvanced = true
		for _, node := range s.readySet {
			s.returned[node.ID] = true
			newlyReady = append(newlyReady, node)
		}
		return newlyReady
	}

	// Mark completed steps and decrement in-degree of their children (one-time).
	// Also propagate skips to children.
	for stepName := range completedSteps {
		if s.processedCompletions[stepName] {
			// Already processed this completion; skip to avoid double-decrement.
			continue
		}
		s.processedCompletions[stepName] = true

		if s.readySet[stepName] != nil {
			delete(s.readySet, stepName)
		}
		delete(s.blocked, stepName)

		// Kahn's algorithm: decrement in-degree of all children.
		for _, childID := range s.dag.Children(stepName) {
			s.dag.UpdateInDegree(childID)
			if s.dag.InDegree(childID) == 0 {
				if node := s.dag.Node(childID); node != nil && !s.returned[childID] {
					s.returned[childID] = true
					s.readySet[childID] = node
					newlyReady = append(newlyReady, node)
				}
			}
		}
	}

	s.propagateSkips()

	// Find any remaining nodes that have become ready (parents completed in same tick).
	// Skip nodes that are already completed or skipped.
	for _, n := range s.dag.Nodes() {
		if s.readySet[n.ID] != nil {
			continue
		}
		if s.blocked[n.ID] {
			continue
		}
		if s.skipped[n.ID] {
			continue
		}
		if s.returned[n.ID] {
			continue
		}
		if s.dag.InDegree(n.ID) == 0 && !completedSteps[n.ID] {
			s.returned[n.ID] = true
			s.readySet[n.ID] = n
			newlyReady = append(newlyReady, n)
		}
	}
	return newlyReady
}

// PendingSlots returns how many more tasks can be started given current claimed count.
func (s *Scheduler) PendingSlots(claimed, pending int) int {
	max := s.maxParallel
	if claimed >= max {
		return 0
	}
	avail := max - claimed
	// On first dispatch (pending==0), don't cap by pending — allow full remaining capacity.
	if pending > 0 && avail > pending {
		avail = pending
	}
	return avail
}

// StepCompleted marks a step as completed so its dependents can become ready.
func (s *Scheduler) StepCompleted(stepName string) {
	if s.readySet[stepName] != nil {
		// Already in ready set; mark as processed by removing from ready.
		delete(s.readySet, stepName)
	}
}

// SkipStep marks a step as skipped due to when condition evaluation.
func (s *Scheduler) SkipStep(stepName string) {
	s.skipped[stepName] = true
	if s.readySet[stepName] != nil {
		delete(s.readySet, stepName)
	}
}

func (s *Scheduler) propagateSkips() {
	queue := make([]string, 0, len(s.skipped))
	seen := make(map[string]bool, len(s.skipped))
	for stepName := range s.skipped {
		queue = append(queue, stepName)
		seen[stepName] = true
	}
	for len(queue) > 0 {
		stepName := queue[0]
		queue = queue[1:]
		delete(s.readySet, stepName)
		for _, childID := range s.dag.Children(stepName) {
			if !s.skipped[childID] {
				s.skipped[childID] = true
				s.dag.UpdateInDegree(childID)
			}
			if !seen[childID] {
				seen[childID] = true
				queue = append(queue, childID)
			}
		}
	}
}

// IsSkipped returns true if the step was skipped.
func (s *Scheduler) IsSkipped(stepName string) bool {
	return s.skipped[stepName]
}

// SkipConditions allows the driver to pre-inject known skip states.
// This is used when a driver already knows certain steps should be skipped.
func (s *Scheduler) SkipConditions(conditions map[string]bool) {
	for stepName, shouldSkip := range conditions {
		if shouldSkip {
			s.skipped[stepName] = true
		}
	}
}

// ReadyCount returns the number of steps currently in the ready set.
func (s *Scheduler) ReadyCount() int {
	return len(s.readySet)
}

// AddReadyStep adds a step to the ready set at runtime (e.g., loop iteration).
func (s *Scheduler) AddReadyStep(node *workflow.Node) {
	s.readySet[node.ID] = node
}

// GetReadySteps returns a snapshot of currently ready steps.
func (s *Scheduler) GetReadySteps() []*workflow.Node {
	nodes := make([]*workflow.Node, 0, len(s.readySet))
	for _, n := range s.readySet {
		if n != nil {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// ─── Checkpointing (Epic 2.8) ─────────────────────────────────────────────────

const checkpointDir = "/tmp/ming-agents-run"

// SchedulerCheckpoint is the persisted state for a scheduler.
type SchedulerCheckpoint struct {
	Version        string                    `json:"version"`
	RunID          uuid.UUID                 `json:"run_id"`
	CompletedSteps map[string]bool           `json:"completed_steps"`
	Skipped        map[string]bool           `json:"skipped"`
	StepOutputs    map[string]map[string]any `json:"step_outputs"`
}

// PersistState persists the scheduler state to a checkpoint file.
// This enables recovery after interrupt/crash.
func (s *Scheduler) PersistState(runID uuid.UUID) error {
	ckpt := SchedulerCheckpoint{
		Version:        "1.0",
		RunID:          runID,
		CompletedSteps: s.completedSteps,
		Skipped:        s.skipped,
		StepOutputs:    s.stepOutputs,
	}

	raw, err := json.Marshal(ckpt)
	if err != nil {
		return fmt.Errorf("serialize checkpoint: %w", err)
	}

	dir := filepath.Join(checkpointDir, runID.String())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create checkpoint dir: %w", err)
	}

	path := filepath.Join(dir, "checkpoint.json")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}

	return nil
}

// RecoverState recovers the scheduler state from a checkpoint file.
// Returns true if a checkpoint was found and recovered, false if none exists.
func (s *Scheduler) RecoverState(runID uuid.UUID) (bool, error) {
	path := filepath.Join(checkpointDir, runID.String(), "checkpoint.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read checkpoint: %w", err)
	}

	var ckpt SchedulerCheckpoint
	if err := json.Unmarshal(data, &ckpt); err != nil {
		return false, fmt.Errorf("parse checkpoint: %w", err)
	}

	s.completedSteps = ckpt.CompletedSteps
	s.skipped = ckpt.Skipped
	s.stepOutputs = ckpt.StepOutputs

	return true, nil
}

// MarkStepCompleted marks a step as completed and records its outputs.
func (s *Scheduler) MarkStepCompleted(stepName string, outputs map[string]any) {
	s.completedSteps[stepName] = true
	if outputs != nil {
		s.stepOutputs[stepName] = outputs
	}
}

// GetCompletedSteps returns the set of completed step names.
func (s *Scheduler) GetCompletedSteps() map[string]bool {
	return s.completedSteps
}

// GetStepOutputs returns the outputs map for a step.
func (s *Scheduler) GetStepOutputs(stepName string) (map[string]any, bool) {
	out, ok := s.stepOutputs[stepName]
	return out, ok
}

// ReplayParams restores resolved parameters from a RunRecord into the scheduler.
// This enables deterministic replay of a run using the recorded parameters.
func (s *Scheduler) ReplayParams(recordedParams map[string]map[string]any) {
	for stepName, params := range recordedParams {
		s.stepOutputs[stepName] = params
	}
}

// SchedulerForRun builds a scheduler from the run's step DAG.
func (e *Engine) SchedulerForRun(run *domain.Run, steps []*domain.Step) (*Scheduler, error) {
	// Build DAG from steps.
	dag := workflow.NewDAG()
	stepMap := make(map[string]*domain.Step)
	for _, st := range steps {
		stepMap[st.Name] = st
		var when *string
		if st.When.Valid {
			when = &st.When.String
		}
		dag.AddNode(&workflow.Node{
			ID:   st.Name,
			Name: st.Name,
			Type: string(st.StepType),
			When: when,
		})
	}
	// Add edges from step inputs referencing other steps.
	for _, st := range steps {
		if !st.InputsJSON.Valid {
			continue
		}
		var inputs map[string]any
		if err := json.Unmarshal([]byte(st.InputsJSON.String), &inputs); err != nil {
			continue
		}
		for _, v := range inputs {
			refs := extractAllRefs(v)
			for _, ref := range refs {
				parts := splitRef(ref)
				if parts == nil {
					continue
				}
				if err := dag.AddEdge(parts[0], st.Name); err != nil {
					return nil, err
				}
			}
		}
	}
	s := NewScheduler(e.store, dag, run.MaxParallel)
	s.InitReadySet()
	return s, nil
}

func extractAllRefs(v any) []string {
	var refs []string
	if s, ok := v.(string); ok {
		re := regexp.MustCompile(`\$\{([^}]+)\}`)
		matches := re.FindAllStringSubmatch(s, -1)
		for _, m := range matches {
			if len(m) > 1 {
				refs = append(refs, m[1])
			}
		}
	}
	return refs
}

func extractRef(v any) string {
	if s, ok := v.(string); ok {
		if len(s) >= 4 && s[:2] == "${" && s[len(s)-1] == '}' {
			return s[2 : len(s)-1]
		}
	}
	return ""
}

func splitRef(ref string) []string {
	for i := 0; i < len(ref); i++ {
		if ref[i] == '.' {
			return []string{ref[:i], ref[i+1:]}
		}
	}
	return nil
}
