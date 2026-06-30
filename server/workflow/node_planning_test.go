package workflow

import (
	"context"
	"strings"
	"testing"
)

func TestPlanningNodeImplementsRollbackCapableNode(t *testing.T) {
	var _ RollbackCapableNode = (*planningNode)(nil)
}

func TestPlanningNodePrepareRollback(t *testing.T) {
	node := &planningNode{}
	rctx := RollbackContext{
		RunID:    "run-1",
		NodeID:   "planning",
		NodeKind: NodeKindPlanning,
		Unit:     RollbackUnit{Scope: "planning", MaxAttempts: 3, ReusePolicy: SessionReuseSameSession},
	}

	decision, err := node.PrepareRollback(context.Background(), rctx, RollbackSignal{
		FailureClass: FailureClassHumanReject,
		Reason:       "split implementation steps",
	})
	if err != nil {
		t.Fatalf("PrepareRollback() error = %v", err)
	}
	if decision.Action != RollbackActionRegeneratePlan {
		t.Fatalf("Action = %q, want %q", decision.Action, RollbackActionRegeneratePlan)
	}
	if !decision.ReuseSession {
		t.Fatal("ReuseSession = false, want true")
	}
	if !strings.Contains(decision.Rationale, "split implementation steps") {
		t.Fatalf("Rationale = %q, want rejection reason", decision.Rationale)
	}
}

func TestPlanningNodeRollbackArtifacts(t *testing.T) {
	node := &planningNode{}
	if artifacts := node.RollbackArtifacts(RollbackContext{Unit: RollbackUnit{Scope: "planning"}}); len(artifacts) != 0 {
		t.Fatalf("RollbackArtifacts() len = %d, want 0", len(artifacts))
	}
}
