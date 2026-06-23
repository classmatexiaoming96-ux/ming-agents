package store

import (
	"database/sql"
	"testing"

	"github.com/ming-agents/server/domain"
	_ "modernc.org/sqlite"
)

type recordingStepNotifier struct {
	changes []StepStatusChange
}

func (n *recordingStepNotifier) OnStepStatusChanged(change StepStatusChange) {
	n.changes = append(n.changes, change)
}

func TestUpdateStepNotifiesWhenStatusChanges(t *testing.T) {
	db := newStoreNotifierTestDB(t)
	st := NewStore(db)
	notifier := &recordingStepNotifier{}
	st.SetStepStatusNotifier(notifier)

	run := &domain.Run{Name: "run", Status: domain.RunStatusRunning, MaxParallel: 1}
	if err := st.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	step := &domain.Step{RunID: run.ID, Name: "code_review", StepType: domain.StepTypeTask, Status: domain.StepStatusPending}
	if err := st.CreateStep(step); err != nil {
		t.Fatalf("create step: %v", err)
	}

	step.Status = domain.StepStatusWaitingUserInput
	if err := st.UpdateStep(step); err != nil {
		t.Fatalf("update step: %v", err)
	}
	if len(notifier.changes) != 1 {
		t.Fatalf("change count = %d, want 1", len(notifier.changes))
	}
	change := notifier.changes[0]
	if change.RunID != run.ID || change.StepID != step.ID {
		t.Fatalf("change ids = (%s, %s), want (%s, %s)", change.RunID, change.StepID, run.ID, step.ID)
	}
	if change.StepName != "code_review" {
		t.Fatalf("step name = %q, want code_review", change.StepName)
	}
	if change.From != domain.StepStatusPending || change.To != domain.StepStatusWaitingUserInput {
		t.Fatalf("change = %s -> %s, want pending -> waiting_user_input", change.From, change.To)
	}

	if err := st.UpdateStep(step); err != nil {
		t.Fatalf("update unchanged step: %v", err)
	}
	if len(notifier.changes) != 1 {
		t.Fatalf("unchanged update emitted %d changes, want 1 total", len(notifier.changes))
	}
}

func newStoreNotifierTestDB(t *testing.T) *sql.DB {
	t.Helper()
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
	return db
}
