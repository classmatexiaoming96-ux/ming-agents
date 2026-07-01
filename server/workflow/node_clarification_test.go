package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ming-agents/server/memory"
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

func TestClarificationNodeExecuteReturnsBriefAudit(t *testing.T) {
	restoreBrief := stubWorkflowBrief(t, "remember requirements", memory.BriefAudit{InjectedIDs: []string{"mem_node_clar"}})
	defer restoreBrief()
	prevRun := runClarificationWithMemoryForNode
	var gotMemory string
	runClarificationWithMemoryForNode = func(ctx context.Context, repoRoot, userInput, memoryBlock string) (string, error) {
		gotMemory = memoryBlock
		path := filepath.Join(repoRoot, "docs", "requirements-clarity.md")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", err
		}
		return path, os.WriteFile(path, []byte("clarification"), 0644)
	}
	defer func() { runClarificationWithMemoryForNode = prevRun }()

	result, err := (&clarificationNode{}).Execute(context.Background(), NodeRequest{
		RunID:    "run-node-clar",
		RepoRoot: t.TempDir(),
		Spec:     NodeSpec{ID: "clarification", Kind: NodeKindClarification},
		Inputs:   NodeInputs{"input": {Values: map[string]any{"user_input": "build brief injection"}}},
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
	if !strings.Contains(gotMemory, "mem_node_clar") {
		t.Fatalf("memoryBlock = %q, want injected ID", gotMemory)
	}
}
