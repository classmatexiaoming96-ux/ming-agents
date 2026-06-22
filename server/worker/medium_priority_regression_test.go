package worker

import (
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
	_ "modernc.org/sqlite"
)

func TestWorkerStopCanBeCalledConcurrently(t *testing.T) {
	w := NewWorker(nil, nil, nil, time.Hour)
	w.Start()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Stop()
		}()
	}
	wg.Wait()
}

func TestAdapterWorkerClaimsOnlyItsAdapterTasks(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	createWorkerSchema(t, db)

	s := store.NewStore(db)
	run := &domain.Run{Name: "run", WDLVersion: "1.0", Status: domain.RunStatusRunning, MaxParallel: 2}
	if err := s.CreateRun(run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	step := &domain.Step{RunID: run.ID, Name: "step", StepType: domain.StepTypeTask, Status: domain.StepStatusRunning}
	if err := s.CreateStep(step); err != nil {
		t.Fatalf("CreateStep() error = %v", err)
	}

	claudeTask := &domain.Task{RunID: run.ID, StepID: step.ID, Status: domain.TaskStatusPending, AdapterKey: "claude-code", AgentRequest: []byte(`{}`), AgentResult: []byte(`{}`)}
	codexTask := &domain.Task{RunID: run.ID, StepID: step.ID, Status: domain.TaskStatusPending, AdapterKey: "codex", AgentRequest: []byte(`{}`), AgentResult: []byte(`{}`)}
	if err := s.CreateTask(claudeTask); err != nil {
		t.Fatalf("CreateTask(claude) error = %v", err)
	}
	if err := s.CreateTask(codexTask); err != nil {
		t.Fatalf("CreateTask(codex) error = %v", err)
	}

	w := NewAdapterWorker(s, stubExecutor{key: "codex"}, nil, time.Hour)
	claimed, err := w.claimTask()
	if err != nil {
		t.Fatalf("claimTask() error = %v", err)
	}
	if claimed.ID != codexTask.ID {
		t.Fatalf("claimed task = %s (%s), want codex task %s", claimed.ID, claimed.AdapterKey, codexTask.ID)
	}
}

type stubExecutor struct {
	key string
}

func (s stubExecutor) Key() string { return s.key }

func (s stubExecutor) Invoke(req adapter.AgentRequest, execCtx ...adapter.ExecutionContext) (*adapter.AgentResult, error) {
	return &adapter.AgentResult{Output: req.Prompt, Summary: "ok"}, nil
}

func createWorkerSchema(t *testing.T, db *sql.DB) {
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
			agent_result BLOB DEFAULT X'7B7D',
			result_summary TEXT,
			claimed_at TIMESTAMP,
			completed_at TIMESTAMP,
			created_at TIMESTAMP,
			version INTEGER
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
}
