package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeResult implements sql.Result with a fixed affected-row count.
type fakeResult struct{ rows int64 }

func (f fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (f fakeResult) RowsAffected() (int64, error) { return f.rows, nil }

// fakeExec records each Exec call and returns a programmable affected-row count
// keyed by which table the statement targets, plus an optional error.
type fakeExec struct {
	calls    []fakeCall
	taskRows int64
	loopRows int64
	errOn    string // substring; if a query contains it, Exec returns errInjected
}

type fakeCall struct {
	query string
	args  []any
}

var errInjected = errors.New("injected")

func (f *fakeExec) Exec(query string, args ...any) (sql.Result, error) {
	f.calls = append(f.calls, fakeCall{query: query, args: args})
	if f.errOn != "" && strings.Contains(query, f.errOn) {
		return nil, errInjected
	}
	switch {
	case strings.Contains(query, "agent_task_queue"):
		return fakeResult{rows: f.taskRows}, nil
	case strings.Contains(query, "loop_iterations"):
		return fakeResult{rows: f.loopRows}, nil
	default:
		return fakeResult{}, nil
	}
}

func TestRetentionOrDefault(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero falls back to default", 0, DefaultRetention},
		{"negative falls back to default", -time.Hour, DefaultRetention},
		{"positive is kept", 48 * time.Hour, 48 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (CleanupConfig{Retention: c.in}).retentionOrDefault(); got != c.want {
				t.Errorf("retentionOrDefault(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestDefaultCleanupConfig(t *testing.T) {
	if got := DefaultCleanupConfig().Retention; got != DefaultRetention {
		t.Errorf("DefaultCleanupConfig().Retention = %v, want %v", got, DefaultRetention)
	}
}

func TestCutoffTime(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	// Default retention: cutoff is 7 days before now.
	gotDefault := DefaultCleanupConfig().cutoffTime(now)
	wantDefault := now.Add(-DefaultRetention)
	if !gotDefault.Equal(wantDefault) {
		t.Errorf("default cutoff = %v, want %v", gotDefault, wantDefault)
	}

	// Custom retention is honored.
	got := CleanupConfig{Retention: 24 * time.Hour}.cutoffTime(now)
	if want := now.Add(-24 * time.Hour); !got.Equal(want) {
		t.Errorf("custom cutoff = %v, want %v", got, want)
	}

	// Non-positive retention falls back to default.
	gotZero := CleanupConfig{}.cutoffTime(now)
	if !gotZero.Equal(wantDefault) {
		t.Errorf("zero-retention cutoff = %v, want %v", gotZero, wantDefault)
	}
}

func TestCleanupExpiredAggregatesAndPassesCutoff(t *testing.T) {
	cutoff := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	fe := &fakeExec{taskRows: 42, loopRows: 7}

	res, err := cleanupExpired(fe, cutoff)
	if err != nil {
		t.Fatalf("cleanupExpired returned error: %v", err)
	}

	if res.TasksDeleted != 42 {
		t.Errorf("TasksDeleted = %d, want 42", res.TasksDeleted)
	}
	if res.LoopIterationsDeleted != 7 {
		t.Errorf("LoopIterationsDeleted = %d, want 7", res.LoopIterationsDeleted)
	}
	if !res.Cutoff.Equal(cutoff) {
		t.Errorf("Cutoff = %v, want %v", res.Cutoff, cutoff)
	}

	if len(fe.calls) != 2 {
		t.Fatalf("expected 2 Exec calls, got %d", len(fe.calls))
	}

	// Both deletes must receive the cutoff as their only argument.
	for i, call := range fe.calls {
		if len(call.args) != 1 {
			t.Fatalf("call %d: expected 1 arg, got %d", i, len(call.args))
		}
		got, ok := call.args[0].(time.Time)
		if !ok || !got.Equal(cutoff) {
			t.Errorf("call %d: arg = %v, want cutoff %v", i, call.args[0], cutoff)
		}
	}

	// One delete per target table.
	if !strings.Contains(fe.calls[0].query, "agent_task_queue") {
		t.Errorf("first delete should target agent_task_queue, got: %s", fe.calls[0].query)
	}
	if !strings.Contains(fe.calls[1].query, "loop_iterations") {
		t.Errorf("second delete should target loop_iterations, got: %s", fe.calls[1].query)
	}
}

// TestCleanupExpiredPolicyPredicates guards the safety policy: only terminal runs
// are touched, and only those older than the cutoff. A regression that drops a
// predicate (e.g. deleting in-flight runs' data) is caught here.
func TestCleanupExpiredPolicyPredicates(t *testing.T) {
	for _, sql := range []string{deleteTasksSQL, deleteLoopIterationsSQL} {
		if !strings.Contains(sql, "r.status IN ('completed','failed','cancelled')") {
			t.Errorf("delete SQL missing terminal-status filter:\n%s", sql)
		}
		for _, live := range []string{"'pending'", "'running'", "'paused'"} {
			if strings.Contains(sql, live) {
				t.Errorf("delete SQL must not reference live status %s:\n%s", live, sql)
			}
		}
		if !strings.Contains(sql, "COALESCE(r.ended_at, r.updated_at) < $1") {
			t.Errorf("delete SQL missing age cutoff predicate:\n%s", sql)
		}
	}
}

func TestCleanupExpiredTaskDeleteError(t *testing.T) {
	fe := &fakeExec{errOn: "agent_task_queue"}
	_, err := cleanupExpired(fe, time.Now())
	if err == nil || !errors.Is(err, errInjected) {
		t.Fatalf("expected injected task-delete error, got %v", err)
	}
	// Loop-iterations delete must not run after the task delete fails.
	if len(fe.calls) != 1 {
		t.Errorf("expected exactly 1 Exec call before error, got %d", len(fe.calls))
	}
}

func TestCleanupExpiredLoopDeleteError(t *testing.T) {
	fe := &fakeExec{taskRows: 3, errOn: "loop_iterations"}
	_, err := cleanupExpired(fe, time.Now())
	if err == nil || !errors.Is(err, errInjected) {
		t.Fatalf("expected injected loop-delete error, got %v", err)
	}
	if len(fe.calls) != 2 {
		t.Errorf("expected 2 Exec calls (task ok, loop fails), got %d", len(fe.calls))
	}
}
