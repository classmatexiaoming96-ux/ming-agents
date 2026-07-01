package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ming-agents/server/memory"
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

func TestPlanningNodeExecuteReturnsBriefAudit(t *testing.T) {
	restoreBrief := stubWorkflowBrief(t, "planning memory", memory.BriefAudit{InjectedIDs: []string{"mem_node_plan"}})
	defer restoreBrief()
	prevRun := runPlanningWithMemoryForNode
	var gotMemory string
	runPlanningWithMemoryForNode = func(ctx context.Context, repoRoot, clarFile, memoryBlock string) (*Plan, error) {
		gotMemory = memoryBlock
		return &Plan{TaskID: "run-node-plan", Subtasks: []Subtask{validSubtask("api")}}, nil
	}
	defer func() { runPlanningWithMemoryForNode = prevRun }()

	result, err := (&planningNode{}).Execute(context.Background(), NodeRequest{
		RunID:    "run-node-plan",
		RepoRoot: t.TempDir(),
		Spec:     NodeSpec{ID: "planning", Kind: NodeKindPlanning},
		Inputs:   NodeInputs{"clarification": {Outputs: map[string]string{"clarification_output": ""}}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.BriefAudit == nil {
		t.Fatal("BriefAudit = nil, want audit")
	}
	if result.BriefPath == "" {
		t.Fatal("BriefPath empty")
	}
	if !strings.Contains(gotMemory, "mem_node_plan") {
		t.Fatalf("memoryBlock = %q, want injected ID", gotMemory)
	}
	if _, ok := result.Values["plan"].(json.RawMessage); !ok {
		t.Fatalf("plan value missing: %#v", result.Values)
	}
}
