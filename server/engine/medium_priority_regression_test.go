package engine

import (
	"database/sql"
	"testing"

	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
	_ "modernc.org/sqlite"
)

func TestTranslatorConditionalUsesContextEvaluateCondition(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("check", "ready", false)

	step := &domain.Step{
		Name:     "conditional",
		StepType: domain.StepTypeConditional,
		InputsMap: map[string]any{
			"_when":  "check.ready == true",
			"prompt": "should be skipped",
		},
	}

	tasks, err := NewTranslator(nil, nil).TranslateStep(step, ctx)
	if err != nil {
		t.Fatalf("TranslateStep() error = %v", err)
	}
	if tasks != nil {
		t.Fatalf("TranslateStep() tasks = %d, want nil for skipped condition", len(tasks))
	}
	if step.Status != domain.StepStatusSkipped {
		t.Fatalf("step status = %s, want skipped", step.Status)
	}
}

func TestTranslatorFanOutRejectsAmbiguousListInputs(t *testing.T) {
	step := &domain.Step{
		Name:     "fanout",
		StepType: domain.StepTypeTask,
		InputsMap: map[string]any{
			"files":   []any{"a.go", "b.go"},
			"patches": []any{"one", "two"},
			"prompt":  "fix ${_item}",
		},
	}

	_, err := NewTranslator(nil, nil).TranslateStep(step, NewContext())
	if err == nil {
		t.Fatal("TranslateStep() error = nil, want ambiguous fan-out error")
	}
}

func TestTranslatorFanOutUsesExplicitListInput(t *testing.T) {
	step := &domain.Step{
		Name:     "fanout",
		StepType: domain.StepTypeTask,
		InputsMap: map[string]any{
			"_list":  []any{"a.go", "b.go"},
			"files":  []any{"not", "the", "source"},
			"prompt": "fix ${_item}",
		},
	}

	tasks, err := NewTranslator(nil, nil).TranslateStep(step, NewContext())
	if err != nil {
		t.Fatalf("TranslateStep() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2 from _list", len(tasks))
	}
}

func TestCompileReturnsCreatedSteps(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}
	s := store.NewStore(db)
	e := NewEngine(s, nil)
	wdl := `{
		"version": "1.0",
		"steps": [
			{"name": "first", "type": "task", "adapter": "fake"},
			{"name": "second", "type": "task", "adapter": "fake", "depends_on": ["first"]}
		]
	}`

	result, err := e.Compile(wdl)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("Compile() returned %d steps, want 2", len(result.Steps))
	}
}
