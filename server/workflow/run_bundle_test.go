package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ming-agents/server/memory"
)

func TestRunBundleReceiver_FreezeOnRunEnd(t *testing.T) {
	oldVault := memory.VaultDir
	memory.VaultDir = t.TempDir()
	t.Cleanup(func() { memory.VaultDir = oldVault })

	registry := NewNodeRegistry()
	registry.Register(NodeKindClarification, func() WorkflowNode {
		return staticNode{kind: NodeKindClarification}
	})
	executor := NewNodeExecutor(registry, NodeServices{})
	spec := WorkflowSpec{
		RunID: "run-freeze",
		Nodes: []NodeSpec{{
			ID:   "clarification",
			Kind: NodeKindClarification,
		}},
	}

	repoRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("MkdirAll repoRoot error = %v", err)
	}
	if _, err := executor.Run(context.Background(), repoRoot, spec, nil); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	root, err := memory.RunBundlePath("repo", "run-freeze")
	if err != nil {
		t.Fatalf("RunBundlePath error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "_frozen")); err != nil {
		t.Fatalf("_frozen missing: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("run bundle root missing: %v", err)
	}
	if got := info.Mode().Perm(); got != 0555 {
		t.Fatalf("run bundle root mode = %v, want 0555", got)
	}
}

func TestFreezePolicy_OnlyOnSuccess(t *testing.T) {
	oldVault := memory.VaultDir
	memory.VaultDir = t.TempDir()
	t.Cleanup(func() { memory.VaultDir = oldVault })

	registry := NewNodeRegistry()
	registry.Register(NodeKindClarification, func() WorkflowNode {
		return staticNode{kind: NodeKindClarification, status: NodeStatusFailed, errText: "boom"}
	})
	executor := NewNodeExecutor(registry, NodeServices{})
	spec := WorkflowSpec{
		RunID: "run-failed-open",
		Nodes: []NodeSpec{{
			ID:   "clarification",
			Kind: NodeKindClarification,
		}},
	}
	repoRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("MkdirAll repoRoot error = %v", err)
	}

	if _, err := executor.Run(context.Background(), repoRoot, spec, nil); err == nil {
		t.Fatal("Run error = nil, want node failure")
	}
	root, err := memory.RunBundlePath("repo", "run-failed-open")
	if err != nil {
		t.Fatalf("RunBundlePath error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "_frozen")); !os.IsNotExist(err) {
		t.Fatalf("_frozen err = %v, want not exist for failed run", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "receiver-status.json"))
	if err != nil {
		t.Fatalf("receiver-status.json missing: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("receiver-status.json decode error = %v", err)
	}
	if status["final_status"] != "incomplete" || status["incomplete_reason"] == "" {
		t.Fatalf("receiver-status.json = %s, want incomplete status", data)
	}
}

func TestIncompleteBundle_AllowsFurtherWrites(t *testing.T) {
	oldVault := memory.VaultDir
	memory.VaultDir = t.TempDir()
	t.Cleanup(func() { memory.VaultDir = oldVault })

	repoRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("MkdirAll repoRoot error = %v", err)
	}
	markRunBundleIncomplete(repoRoot, "run-incomplete", errors.New("node failed"))

	receiver, err := memory.NewRunBundleReceiver("repo", "run-incomplete")
	if err != nil {
		t.Fatalf("NewRunBundleReceiver error = %v", err)
	}
	if err := receiver.ReceivePhaseReuse("diagnostics", "late diagnostic"); err != nil {
		t.Fatalf("ReceivePhaseReuse after incomplete error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(receiver.Root(), "phase-reuse", "diagnostics.md")); err != nil {
		t.Fatalf("late diagnostic missing: %v", err)
	}
}

func TestMirrorReuseAck_RejectsPhaseMismatch(t *testing.T) {
	oldVault := memory.VaultDir
	memory.VaultDir = t.TempDir()
	t.Cleanup(func() { memory.VaultDir = oldVault })

	repoRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("MkdirAll repoRoot error = %v", err)
	}
	req := NodeRequest{
		RunID:    "run-ack",
		RepoRoot: repoRoot,
		Spec:     NodeSpec{ID: "planning", Kind: NodeKindPlanning},
	}

	mirrorReuseAckToRunBundle(req, "planning", ReuseAck{
		RunID:    "run-ack",
		Phase:    "clarification",
		Accepted: true,
	})

	root, err := memory.RunBundlePath("repo", "run-ack")
	if err != nil {
		t.Fatalf("RunBundlePath error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "reuse-ack", "planning.json")); !os.IsNotExist(err) {
		t.Fatalf("phase-mismatched reuse ack write err = %v, want not exist", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "receiver-status.json"))
	if err != nil {
		t.Fatalf("receiver-status.json missing: %v", err)
	}
	var status map[string]struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("receiver-status.json decode error = %v", err)
	}
	if status["reuse_ack"].Status != "skipped" || status["reuse_ack"].Reason == "" {
		t.Fatalf("reuse_ack status = %+v, want skipped with reason", status["reuse_ack"])
	}
}

func TestRunBundleEndToEnd_IsolatedFromL2AndArchive(t *testing.T) {
	oldVault := memory.VaultDir
	memory.VaultDir = t.TempDir()
	t.Cleanup(func() { memory.VaultDir = oldVault })
	restoreBrief := stubWorkflowBrief(t, "memory", memory.BriefAudit{InjectedIDs: []string{"mem-e2e"}})
	defer restoreBrief()

	prevClar := runClarificationWithMemoryForNode
	runClarificationWithMemoryForNode = func(ctx context.Context, repoRoot, userInput, memoryBlock string) (string, error) {
		path := filepath.Join(repoRoot, ".workflow", "runs", "run-e2e", "clarification.md")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", err
		}
		return path, os.WriteFile(path, []byte("clarified"), 0644)
	}
	defer func() { runClarificationWithMemoryForNode = prevClar }()

	prevPlan := runPlanningWithMemoryForNode
	runPlanningWithMemoryForNode = func(ctx context.Context, repoRoot, clarFile, memoryBlock string) (*Plan, error) {
		return &Plan{TaskID: "run-e2e", Subtasks: []Subtask{validSubtask("api")}}, nil
	}
	defer func() { runPlanningWithMemoryForNode = prevPlan }()

	prevDev := runDevelopmentOnlyWithMemoryForNode
	runDevelopmentOnlyWithMemoryForNode = func(ctx context.Context, repoRoot string, plan *Plan, memoryBySubtask map[string]string) (*WorkflowState, error) {
		return &WorkflowState{
			RunID:   plan.TaskID,
			Nodes:   map[string]NodeStatus{"development": NodeStatusCompleted},
			Details: map[string]any{"subtask_results": []*SubtaskResult{{Subtask: plan.Subtasks[0], Status: "completed", Output: "done"}}},
		}, nil
	}
	defer func() { runDevelopmentOnlyWithMemoryForNode = prevDev }()

	prevSubReview := runSubtaskReviewWithMemoryForNode
	runSubtaskReviewWithMemoryForNode = func(ctx context.Context, repoRoot, runID string, plan *Plan, result *SubtaskResult, diffFile, memoryBlock string) (*ReviewReport, string, ReviewSubtaskPaths, error) {
		return &ReviewReport{Passed: true}, "review", ReviewSubtaskPaths{}, nil
	}
	defer func() { runSubtaskReviewWithMemoryForNode = prevSubReview }()

	prevAggReview := runAggregateReviewWithMemoryForNode
	runAggregateReviewWithMemoryForNode = func(ctx context.Context, repoRoot, runID string, plan *Plan, reports map[string]*ReviewReport, memoryBlock string) (*ReviewReport, string, ReviewAggregatePaths, error) {
		return &ReviewReport{Passed: true}, "aggregate", ReviewAggregatePaths{}, nil
	}
	defer func() { runAggregateReviewWithMemoryForNode = prevAggReview }()

	prevEval := runEvaluationWithPlanForNode
	runEvaluationWithPlanForNode = func(ctx context.Context, repoRoot, runID string, plan *Plan) (*EvaluationResult, error) {
		runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
		if err := os.MkdirAll(runDir, 0755); err != nil {
			return nil, err
		}
		testLog := filepath.Join(runDir, "test.log")
		if err := os.WriteFile(testLog, []byte("ok"), 0644); err != nil {
			return nil, err
		}
		receiver, err := memory.NewRunBundleReceiver(projectFromRepoRoot(repoRoot), runID)
		if err != nil {
			return nil, err
		}
		if err := receiver.ReceiveAutoMindSummary([]byte("summary"), "md"); err != nil {
			return nil, err
		}
		return &EvaluationResult{RunID: runID, Passed: true, Evidence: []EvidenceRef{{Type: EvidenceTypeTestLog, Path: testLog}}}, nil
	}
	defer func() { runEvaluationWithPlanForNode = prevEval }()

	repoRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("MkdirAll repoRoot error = %v", err)
	}
	spec := WorkflowSpec{RunID: "run-e2e", Nodes: []NodeSpec{
		{ID: "clarification", Kind: NodeKindClarification},
		{ID: "planning", Kind: NodeKindPlanning, DependsOn: []string{"clarification"}},
		{ID: "development", Kind: NodeKindDevelopment, DependsOn: []string{"planning"}},
		{ID: "review", Kind: NodeKindReview, DependsOn: []string{"planning", "development"}},
		{ID: "evaluation", Kind: NodeKindEvaluation, DependsOn: []string{"planning", "review"}},
	}}
	initial := NodeInputs{"input": {Values: map[string]any{"user_input": "ship phase 5"}}}
	registry := NewNodeRegistry()
	registry.Register(NodeKindClarification, func() WorkflowNode { return &clarificationNode{} })
	registry.Register(NodeKindPlanning, func() WorkflowNode { return &planningNode{} })
	registry.Register(NodeKindDevelopment, func() WorkflowNode { return &developmentNode{} })
	registry.Register(NodeKindReview, func() WorkflowNode { return &reviewNode{} })
	registry.Register(NodeKindEvaluation, func() WorkflowNode { return &evaluationNode{} })
	if _, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), repoRoot, spec, initial); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	root, err := memory.RunBundlePath("repo", "run-e2e")
	if err != nil {
		t.Fatalf("RunBundlePath error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "_frozen")); err != nil {
		t.Fatalf("_frozen missing: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	var manifest struct {
		ArtifactCounts map[string]int `json:"artifact_counts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest decode error = %v", err)
	}
	for _, key := range []string{"phase_reuse", "reuse_ack", "brief_audit", "evidence_pointers", "automind_summary"} {
		if manifest.ArtifactCounts[key] == 0 {
			t.Fatalf("artifact_counts[%s] = 0 in %+v", key, manifest.ArtifactCounts)
		}
	}
	if _, err := os.Stat(filepath.Join(memory.VaultDir, "notes", "repo", "phase-reuse")); !os.IsNotExist(err) {
		t.Fatalf("L2 notes contains raw phase reuse spam, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(memory.VaultDir, "archive", "repo", "run-e2e")); !os.IsNotExist(err) {
		t.Fatalf("archive contains run bundle, err=%v", err)
	}
}

type staticNode struct {
	kind    NodeKind
	status  NodeStatus
	errText string
}

func (n staticNode) Kind() NodeKind { return n.kind }

func (n staticNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	status := n.status
	if status == "" {
		status = NodeStatusCompleted
	}
	return &NodeResult{NodeID: req.Spec.ID, Status: status, Error: n.errText}, nil
}
