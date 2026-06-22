package engine

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
	"github.com/ming-agents/server/workflow"
	_ "modernc.org/sqlite"
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

func TestTranslatorFanOutRendersPerTaskExecutionContext(t *testing.T) {
	step := &domain.Step{
		ID:         uuid.New(),
		RunID:      uuid.New(),
		Name:       "fix",
		StepType:   domain.StepTypeTask,
		AdapterKey: "codex",
		Status:     domain.StepStatusPending,
		Attempt:    1,
		InputsJSON: sql.NullString{Valid: true, String: `{
			"files": ["a.go", "b.go"],
			"prompt": "Fix ${_item}",
			"work_dir": "/tmp/work-${_index}",
			"command": "/bin/echo",
			"timeout_ms": 2500
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
		var req adapter.AgentRequest
		if err := json.Unmarshal(task.AgentRequest, &req); err != nil {
			t.Fatalf("unmarshal request %d: %v", i, err)
		}
		if want := "/tmp/work-" + string(rune('0'+i)); req.Execution.WorkDir != want {
			t.Fatalf("task %d work_dir = %q, want %q", i, req.Execution.WorkDir, want)
		}
		if req.Execution.Command != "/bin/echo" {
			t.Fatalf("task %d command = %q, want /bin/echo", i, req.Execution.Command)
		}
		if req.Execution.Timeout != 2500*time.Millisecond {
			t.Fatalf("task %d timeout = %s, want 2.5s", i, req.Execution.Timeout)
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

func TestRunDriverOnTaskCompletedIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	createDriverCallbackSchema(t, db)

	s := store.NewStore(db)
	run := &domain.Run{
		Name:        "race",
		WDLVersion:  "1.0",
		Status:      domain.RunStatusPending,
		MaxParallel: 2,
	}
	if err := s.CreateRun(run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	step := &domain.Step{
		RunID:    run.ID,
		Name:     "fanout",
		StepType: domain.StepTypeTask,
		Status:   domain.StepStatusRunning,
		Attempt:  1,
	}
	if err := s.CreateStep(step); err != nil {
		t.Fatalf("CreateStep() error = %v", err)
	}
	tasks := []*domain.Task{
		{RunID: run.ID, StepID: step.ID, Status: domain.TaskStatusPending, AdapterKey: "fake", AgentRequest: mustJSON(t, adapter.AgentRequest{Prompt: "a"})},
		{RunID: run.ID, StepID: step.ID, Iteration: 1, Status: domain.TaskStatusPending, AdapterKey: "fake", AgentRequest: mustJSON(t, adapter.AgentRequest{Prompt: "b"})},
	}
	if err := s.CreateTasks(tasks); err != nil {
		t.Fatalf("CreateTasks() error = %v", err)
	}
	for i, task := range tasks {
		if err := s.SetTaskResult(task.ID, mustJSON(t, adapter.AgentResult{Output: []string{"a", "b"}[i]}), "done"); err != nil {
			t.Fatalf("SetTaskResult(%d) error = %v", i, err)
		}
	}

	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "fanout", Name: "fanout"})
	driver := NewRunDriver(s, nil, NewEngine(s, nil))
	runner, err := driver.createRunner(run.ID)
	if err != nil {
		t.Fatalf("createRunner() error = %v", err)
	}
	runner.scheduler = NewScheduler(s, dag, run.MaxParallel)
	runner.scheduler.InitReadySet()

	if err := driver.OnTaskCompleted(tasks[0].ID); err != nil {
		t.Fatalf("first OnTaskCompleted() error = %v", err)
	}
	if err := driver.OnTaskCompleted(tasks[1].ID); err != nil {
		t.Fatalf("second OnTaskCompleted() error = %v", err)
	}

	var snapshots int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artifacts WHERE name='_run_snapshot'`).Scan(&snapshots); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if snapshots != 1 {
		t.Fatalf("snapshot count = %d, want 1 after duplicate completion callbacks", snapshots)
	}
}

func TestRunDriverRoutesTaskCallbacksToPerRunRunners(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	createDriverCallbackSchema(t, db)

	s := store.NewStore(db)
	driver := NewRunDriver(s, nil, NewEngine(s, nil))
	for _, name := range []string{"run-a", "run-b"} {
		run := &domain.Run{
			Name:        name,
			WDLVersion:  "1.0",
			Status:      domain.RunStatusPending,
			MaxParallel: 1,
		}
		if err := s.CreateRun(run); err != nil {
			t.Fatalf("CreateRun(%s) error = %v", name, err)
		}
		step := &domain.Step{
			RunID:    run.ID,
			Name:     "step-" + name,
			StepType: domain.StepTypeTask,
			Status:   domain.StepStatusRunning,
			Attempt:  1,
		}
		if err := s.CreateStep(step); err != nil {
			t.Fatalf("CreateStep(%s) error = %v", name, err)
		}
		task := &domain.Task{
			RunID:        run.ID,
			StepID:       step.ID,
			Status:       domain.TaskStatusPending,
			AdapterKey:   "fake",
			AgentRequest: mustJSON(t, adapter.AgentRequest{Prompt: name}),
		}
		if err := s.CreateTask(task); err != nil {
			t.Fatalf("CreateTask(%s) error = %v", name, err)
		}
		if err := s.SetTaskResult(task.ID, mustJSON(t, adapter.AgentResult{Output: name}), "done"); err != nil {
			t.Fatalf("SetTaskResult(%s) error = %v", name, err)
		}

		dag := workflow.NewDAG()
		dag.AddNode(&workflow.Node{ID: step.Name, Name: step.Name})
		runner, err := driver.createRunner(run.ID)
		if err != nil {
			t.Fatalf("createRunner(%s) error = %v", name, err)
		}
		runner.scheduler = NewScheduler(s, dag, run.MaxParallel)
		runner.scheduler.InitReadySet()

		if err := driver.OnTaskCompleted(task.ID); err != nil {
			t.Fatalf("OnTaskCompleted(%s) error = %v", name, err)
		}
		if !runner.completed[step.Name] {
			t.Fatalf("runner for %s did not complete its own step", name)
		}
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

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func createDriverCallbackSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE runs (
			id TEXT PRIMARY KEY,
			name TEXT,
			wdl_version TEXT,
			status TEXT,
			wdl_src TEXT,
			max_parallel INTEGER,
			created_at TIMESTAMP,
			updated_at TIMESTAMP,
			ended_at TIMESTAMP,
			error_msg TEXT,
			version INTEGER
		)`,
		`CREATE TABLE steps (
			id TEXT PRIMARY KEY,
			run_id TEXT,
			name TEXT,
			step_type TEXT,
			status TEXT,
			iteration INTEGER,
			attempt INTEGER,
			when_cond TEXT,
			inputs_json TEXT,
			outputs_json TEXT,
			skip_reason TEXT,
			created_at TIMESTAMP,
			updated_at TIMESTAMP
		)`,
		`CREATE TABLE agent_task_queue (
			id TEXT PRIMARY KEY,
			run_id TEXT,
			step_id TEXT,
			iteration INTEGER,
			attempt INTEGER,
			status TEXT,
			adapter_key TEXT,
			agent_request BLOB,
			agent_result BLOB,
			result_summary TEXT,
			claimed_at TIMESTAMP,
			completed_at TIMESTAMP,
			created_at TIMESTAMP,
			version INTEGER
		)`,
		`CREATE TABLE artifacts (
			id TEXT PRIMARY KEY,
			run_id TEXT,
			step_id TEXT,
			name TEXT,
			type TEXT,
			content TEXT,
			created_at TIMESTAMP
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
}
