package engine

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/domain"
)

func TestTranslatorFanOutHydratesDBInputsAndRendersPerItemPrompt(t *testing.T) {
	step := &domain.Step{
		ID:         uuid.New(),
		RunID:      uuid.New(),
		Name:       "fix",
		StepType:   domain.StepTypeTask,
		AdapterKey: "claude",
		Status:     domain.StepStatusPending,
		Attempt:    1,
		InputsJSON: sql.NullString{Valid: true, String: `{
			"files": ["a.go", "b.go"],
			"prompt": "Fix ${_item} (${_index}/${_total})"
		}`},
	}

	tasks, err := NewTranslator(nil, nil).TranslateStep(step, NewContext())
	if err != nil {
		t.Fatalf("TranslateStep() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tasks))
	}

	for i, task := range tasks {
		if task.Iteration != i {
			t.Fatalf("task %d iteration = %d, want %d", i, task.Iteration, i)
		}
		var req adapter.AgentRequest
		if err := json.Unmarshal(task.AgentRequest, &req); err != nil {
			t.Fatalf("unmarshal request %d: %v", i, err)
		}
		wantFile := []string{"a.go", "b.go"}[i]
		if !strings.Contains(req.Prompt, wantFile) || strings.Contains(req.Prompt, "${_item}") {
			t.Fatalf("task %d prompt = %q, want rendered item %q", i, req.Prompt, wantFile)
		}
	}
}

func TestAggregateTaskOutputsOrdersFanOutByIteration(t *testing.T) {
	step := &domain.Step{ID: uuid.New(), Name: "fanout"}
	tasks := []*domain.Task{
		completedTask(t, step.ID, 2, "third"),
		completedTask(t, step.ID, 0, "first"),
		completedTask(t, step.ID, 1, "second"),
	}

	outputs := aggregateTaskOutputs(step, tasks)
	results, ok := outputs["results"].([]map[string]any)
	if !ok {
		t.Fatalf("results = %T, want []map[string]any", outputs["results"])
	}
	for i, want := range []string{"first", "second", "third"} {
		if got := results[i]["result"]; got != want {
			t.Fatalf("results[%d].result = %v, want %q", i, got, want)
		}
	}
}

func TestSchedulerForRunReturnsAddEdgeError(t *testing.T) {
	engine := NewEngine(nil, nil)
	run := &domain.Run{ID: uuid.New(), MaxParallel: 1}
	steps := []*domain.Step{{
		ID:         uuid.New(),
		RunID:      run.ID,
		Name:       "only",
		StepType:   domain.StepTypeTask,
		InputsJSON: sql.NullString{Valid: true, String: `{"input":"${missing.value}"}`},
	}}

	if _, err := engine.SchedulerForRun(run, steps); err == nil {
		t.Fatal("SchedulerForRun() error = nil, want AddEdge error for missing upstream step")
	}
}

func TestRuntimeOutputsForStepUsesTaskResultInsteadOfStepOutputsJSON(t *testing.T) {
	step := &domain.Step{
		ID:          uuid.New(),
		Name:        "done",
		Status:      domain.StepStatusCompleted,
		OutputsJSON: sql.NullString{Valid: true, String: `{"result":"stale"}`},
	}
	task := completedTask(t, step.ID, 0, "runtime")

	outputs, ok := runtimeOutputsForStep(step, []*domain.Task{task})
	if !ok {
		t.Fatal("runtimeOutputsForStep() ok = false, want true")
	}
	if got := outputs["result"]; got != "runtime" {
		t.Fatalf("result = %v, want runtime task output", got)
	}
}

func completedTask(t *testing.T, stepID uuid.UUID, iteration int, output string) *domain.Task {
	t.Helper()
	raw, err := json.Marshal(adapter.AgentResult{Output: output})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return &domain.Task{
		ID:          uuid.New(),
		StepID:      stepID,
		Iteration:   iteration,
		Status:      domain.TaskStatusCompleted,
		AgentResult: raw,
	}
}
