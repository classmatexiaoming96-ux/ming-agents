package adapter

import (
	"testing"
	"time"
)

func TestPTYSessionRegistryRegistersListsUpdatesAndUnregisters(t *testing.T) {
	registry := NewPTYSessionRegistry()
	createdAt := time.Now().Add(-time.Minute)
	registry.Register(&PTYSessionRecord{
		SessionID: "session-1",
		RunID:     "run-1",
		StepID:    "step-1",
		TaskID:    "task-1",
		NodeName:  "node3",
		AgentType: "codex",
		Status:    PTYSessionStatusStarting,
		CreatedAt: createdAt,
	})
	registry.Register(&PTYSessionRecord{
		SessionID: "session-2",
		RunID:     "run-2",
		AgentType: "claude-code",
		Status:    PTYSessionStatusRunning,
	})

	rec, ok := registry.Get("session-1")
	if !ok {
		t.Fatal("Get(session-1) ok = false, want true")
	}
	if rec.RunID != "run-1" || rec.CreatedAt.IsZero() || rec.UpdatedAt.IsZero() {
		t.Fatalf("registered record = %+v, want run and timestamps populated", rec)
	}

	byRun := registry.ListByRun("run-1")
	if len(byRun) != 1 || byRun[0].SessionID != "session-1" {
		t.Fatalf("ListByRun(run-1) = %+v, want only session-1", byRun)
	}

	registry.UpdateStatus("session-1", PTYSessionStatusRunning)
	rec, ok = registry.Get("session-1")
	if !ok {
		t.Fatal("Get(session-1) after update ok = false, want true")
	}
	if rec.Status != PTYSessionStatusRunning {
		t.Fatalf("status = %q, want %q", rec.Status, PTYSessionStatusRunning)
	}

	registry.Unregister("session-1")
	if _, ok := registry.Get("session-1"); ok {
		t.Fatal("Get(session-1) ok = true after unregister, want false")
	}
}
