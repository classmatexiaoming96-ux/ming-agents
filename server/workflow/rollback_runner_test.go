package workflow

import (
	"testing"
)

type memoryLineageStore struct {
	events []AttemptEvent
}

func (s *memoryLineageStore) Append(event AttemptEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *memoryLineageStore) List(filter AttemptFilter) ([]AttemptEvent, error) {
	var out []AttemptEvent
	for _, event := range s.events {
		if filter.RunID != "" && event.RunID != filter.RunID {
			continue
		}
		if filter.NodeID != "" && event.NodeID != filter.NodeID {
			continue
		}
		if filter.Scope != "" && event.Scope != filter.Scope {
			continue
		}
		if filter.SubtaskID != "" && event.SubtaskID != filter.SubtaskID {
			continue
		}
		if event.Attempt < filter.FromAttempt {
			continue
		}
		out = append(out, event)
	}
	return out, nil
}

func TestRollbackRunnerDefaultBudgetAllowsThreeAttempts(t *testing.T) {
	runner := NewRollbackRunner()
	spec := RollbackSpec{
		OnHumanReject: RollbackActionFixClarification,
	}
	unit := RollbackUnit{Scope: "clarification"}
	decision := runner.Decide(RollbackContext{
		RunID:    "run-1",
		NodeID:   "clarification",
		NodeKind: NodeKindClarification,
		Unit:     unit,
	}, spec, unit, nil, RollbackSignal{FailureClass: FailureClassHumanReject, Reason: "needs more detail"})

	if decision.Action != RollbackActionFixClarification {
		t.Fatalf("Action = %q, want %q", decision.Action, RollbackActionFixClarification)
	}
	if decision.NewAttempt != 1 {
		t.Fatalf("NewAttempt = %d, want 1", decision.NewAttempt)
	}
	if decision.TargetScope != "clarification" {
		t.Fatalf("TargetScope = %q, want clarification", decision.TargetScope)
	}
}

func TestRollbackRunnerPerScopeBudget(t *testing.T) {
	runner := NewRollbackRunner()
	spec := RollbackSpec{OnProductDefect: RollbackActionRetrySubtask}
	unit := RollbackUnit{Scope: "subtask:api", MaxAttempts: 2}
	attempts := []AttemptEvent{
		{RunID: "run-1", NodeID: "development", Scope: "subtask:api", Attempt: 0},
		{RunID: "run-1", NodeID: "development", Scope: "subtask:web", Attempt: 0},
		{RunID: "run-1", NodeID: "development", Scope: "subtask:web", Attempt: 1},
	}

	decision := runner.Decide(RollbackContext{
		RunID:  "run-1",
		NodeID: "development",
		Unit:   unit,
	}, spec, unit, attempts, RollbackSignal{FailureClass: FailureClassProductDefect})

	if decision.Action != RollbackActionRetrySubtask {
		t.Fatalf("Action = %q, want %q", decision.Action, RollbackActionRetrySubtask)
	}
	if decision.NewAttempt != 1 {
		t.Fatalf("NewAttempt = %d, want 1", decision.NewAttempt)
	}
}

func TestRollbackRunnerNoMatchingActionBlocks(t *testing.T) {
	runner := NewRollbackRunner()
	unit := RollbackUnit{Scope: "planning", MaxAttempts: 3}

	decision := runner.Decide(RollbackContext{
		RunID:  "run-1",
		NodeID: "planning",
		Unit:   unit,
	}, RollbackSpec{}, unit, nil, RollbackSignal{FailureClass: FailureClassContractError})

	if decision.Action != RollbackActionBlocked {
		t.Fatalf("Action = %q, want %q", decision.Action, RollbackActionBlocked)
	}
	if decision.NewAttempt != 0 {
		t.Fatalf("NewAttempt = %d, want 0", decision.NewAttempt)
	}
}

func TestRollbackRunnerBudgetExhaustedUsesConfiguredAction(t *testing.T) {
	runner := NewRollbackRunner()
	spec := RollbackSpec{OnHumanReject: RollbackActionRegeneratePlan}
	unit := RollbackUnit{Scope: "planning", MaxAttempts: 2}
	attempts := []AttemptEvent{
		{RunID: "run-1", NodeID: "planning", Scope: "planning", Attempt: 0},
		{RunID: "run-1", NodeID: "planning", Scope: "planning", Attempt: 1},
	}

	decision := runner.Decide(RollbackContext{
		RunID:  "run-1",
		NodeID: "planning",
		Unit:   unit,
		Budget: RollbackBudget{ExhaustedAction: RollbackActionAskUser},
	}, spec, unit, attempts, RollbackSignal{FailureClass: FailureClassHumanReject})

	if decision.Action != RollbackActionAskUser {
		t.Fatalf("Action = %q, want %q", decision.Action, RollbackActionAskUser)
	}
	if decision.NewAttempt != 2 {
		t.Fatalf("NewAttempt = %d, want 2", decision.NewAttempt)
	}
	if !decision.RetryExhausted {
		t.Fatal("RetryExhausted = false, want true")
	}
	if decision.FailureClass != FailureClassHumanReject {
		t.Fatalf("FailureClass = %q, want %q", decision.FailureClass, FailureClassHumanReject)
	}
	if decision.NextAction != "ask_user" {
		t.Fatalf("NextAction = %q, want ask_user", decision.NextAction)
	}
	if decision.FailureReason == "" {
		t.Fatal("FailureReason is empty, want exhausted reason")
	}
}

func TestRollbackRunnerRecordRollbackEvent(t *testing.T) {
	store := &memoryLineageStore{}
	runner := NewRollbackRunner()
	decision := &RollbackDecision{
		Action:      RollbackActionRetrySubtask,
		TargetScope: "subtask:api",
		NewAttempt:  2,
		Rationale:   "retry product defect",
	}
	rctx := RollbackContext{
		RunID:    "run-1",
		NodeID:   "development",
		NodeKind: NodeKindDevelopment,
		Unit:     RollbackUnit{Scope: "subtask:api"},
		Lineage:  store,
	}

	if err := runner.RecordRollbackEvent(rctx, decision); err != nil {
		t.Fatalf("RecordRollbackEvent() error = %v", err)
	}
	if len(store.events) != 1 {
		t.Fatalf("recorded events = %d, want 1", len(store.events))
	}
	event := store.events[0]
	if event.Decision == nil || event.Decision.Action != RollbackActionRetrySubtask {
		t.Fatalf("recorded decision = %#v, want retry_subtask", event.Decision)
	}
	if event.Attempt != 2 {
		t.Fatalf("Attempt = %d, want 2", event.Attempt)
	}
	if event.Scope != "subtask:api" {
		t.Fatalf("Scope = %q, want subtask:api", event.Scope)
	}
}
