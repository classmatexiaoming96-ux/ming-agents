package workflow

import (
	"context"
	"strings"
	"testing"
)

func TestClarificationNodeImplementsRollbackCapableNode(t *testing.T) {
	var _ RollbackCapableNode = (*clarificationNode)(nil)
}

func TestClarificationNodePrepareRollback(t *testing.T) {
	node := &clarificationNode{}
	rctx := RollbackContext{
		RunID:    "run-1",
		NodeID:   "clarification",
		NodeKind: NodeKindClarification,
		Unit:     RollbackUnit{Scope: "clarification", MaxAttempts: 3, ReusePolicy: SessionReuseSameSession},
	}

	decision, err := node.PrepareRollback(context.Background(), rctx, RollbackSignal{
		FailureClass: FailureClassHumanReject,
		Reason:       "add edge cases",
	})
	if err != nil {
		t.Fatalf("PrepareRollback() error = %v", err)
	}
	if decision.Action != RollbackActionFixClarification {
		t.Fatalf("Action = %q, want %q", decision.Action, RollbackActionFixClarification)
	}
	if !decision.ReuseSession {
		t.Fatal("ReuseSession = false, want true")
	}
	if !strings.Contains(decision.Rationale, "add edge cases") {
		t.Fatalf("Rationale = %q, want rejection reason", decision.Rationale)
	}
}

func TestClarificationNodeRollbackArtifacts(t *testing.T) {
	node := &clarificationNode{}
	artifacts := node.RollbackArtifacts(RollbackContext{Unit: RollbackUnit{Scope: "clarification"}})
	if len(artifacts) != 0 {
		t.Fatalf("RollbackArtifacts() len = %d, want 0", len(artifacts))
	}
}
