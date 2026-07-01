package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ming-agents/server/memory"
)

func TestDevelopmentNodeImplementsRollbackCapableNode(t *testing.T) {
	var _ RollbackCapableNode = (*developmentNode)(nil)
}

func TestDevelopmentNodePrepareRollback(t *testing.T) {
	node := &developmentNode{}
	rctx := RollbackContext{
		RunID:    "run-1",
		NodeID:   "development",
		NodeKind: NodeKindDevelopment,
		Unit:     RollbackUnit{Scope: "subtask:api", MaxAttempts: 3, ReusePolicy: SessionReuseOnHumanReject},
	}

	decision, err := node.PrepareRollback(context.Background(), rctx, RollbackSignal{
		FailureClass: FailureClassHumanReject,
		Reason:       "cover validation",
	})
	if err != nil {
		t.Fatalf("PrepareRollback() error = %v", err)
	}
	if decision.Action != RollbackActionRetrySubtask {
		t.Fatalf("Action = %q, want %q", decision.Action, RollbackActionRetrySubtask)
	}
	if !decision.ReuseSession {
		t.Fatal("ReuseSession = false, want true")
	}
	if !strings.Contains(decision.Rationale, "cover validation") {
		t.Fatalf("Rationale = %q, want feedback", decision.Rationale)
	}
}

func TestDevelopmentNodeIssuesBySubtask(t *testing.T) {
	results := []*SubtaskResult{
		{Subtask: Subtask{ID: "api"}, SessionID: "session-api"},
		{Subtask: Subtask{ID: "web"}, SessionID: "session-web"},
	}
	issues := []ReviewIssue{
		{SubtaskID: "api", Severity: "blocking", Description: "direct"},
		{SessionID: "session-web", Severity: "BLOCKING", Description: "session"},
		{Severity: "blocking", Description: "fallback"},
		{SubtaskID: "api", Severity: "warning", Description: "skip"},
	}

	got := IssuesBySubtask(issues, results)
	if len(got["api"]) != 2 {
		t.Fatalf("api issues = %#v, want direct plus fallback", got["api"])
	}
	if got["api"][0].Description != "direct" || got["api"][1].Description != "fallback" {
		t.Fatalf("api issue order = %#v", got["api"])
	}
	if len(got["web"]) != 1 || got["web"][0].Description != "session" {
		t.Fatalf("web issues = %#v, want session issue", got["web"])
	}
}

func TestDevelopmentNodeRunsOnlyModeWithoutConfigFlag(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "workflow"), 0755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	commandDir := t.TempDir()
	codexPath := filepath.Join(commandDir, "codex")
	if err := os.WriteFile(codexPath, []byte(`#!/bin/sh
printf 'OpenAI Codex\n› '
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'development done\n'
      printf '%s\n' "$marker"
      ;;
  esac
done
`), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", commandDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	plan := &Plan{TaskID: "run-node-dev", Subtasks: []Subtask{validSubtask("api")}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal(plan) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	approvalDone := make(chan error, 1)
	go func() {
		sessionID := NewPTYSessionID(plan.TaskID, "node3", "subtask-api", 1)
		historyFile := filepath.Join(repoRoot, ".workflow", "runs", plan.TaskID, "node3", "agents", "api.messages.jsonl")
		waitForHistoryRoleCount(t, historyFile, "approval_request", 1)
		approvalDone <- ApproveSession(sessionID, "subtask:api", "approved")
	}()

	result, err := (&developmentNode{}).Execute(ctx, NodeRequest{
		RunID:    plan.TaskID,
		RepoRoot: repoRoot,
		Spec:     NodeSpec{ID: "development", Kind: NodeKindDevelopment},
		Inputs: NodeInputs{
			"planning": {NodeID: "planning", Values: map[string]any{"plan": json.RawMessage(planJSON)}},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != NodeStatusCompleted {
		t.Fatalf("Status = %s, want %s; error=%s", result.Status, NodeStatusCompleted, result.Error)
	}
	if err := <-approvalDone; err != nil {
		t.Fatalf("approval error = %v", err)
	}
	if _, ok := result.Values["subtask_results"]; !ok {
		t.Fatal("development node result missing subtask_results")
	}
	state, _ := result.Values["state"].(*WorkflowState)
	if state == nil {
		t.Fatal("development node result missing state")
	}
	if _, ok := state.Details["subtask_results"]; !ok {
		t.Fatal("development state details missing subtask_results")
	}

	runDir := filepath.Join(repoRoot, ".workflow", "runs", plan.TaskID)
	if _, err := os.Stat(filepath.Join(runDir, "development", "review.out.md")); !os.IsNotExist(err) {
		t.Fatalf("development node wrote review output, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "evaluation.json")); !os.IsNotExist(err) {
		t.Fatalf("development node wrote evaluation output, err=%v", err)
	}
}

func TestDevelopmentNodeExecuteReturnsBriefAudit(t *testing.T) {
	restoreBrief := stubWorkflowBrief(t, "development memory", memory.BriefAudit{InjectedIDs: []string{"mem_node_dev"}})
	defer restoreBrief()
	prevRun := runDevelopmentOnlyWithMemoryForNode
	var gotMemory string
	runDevelopmentOnlyWithMemoryForNode = func(ctx context.Context, repoRoot string, plan *Plan, memoryBySubtask map[string]string) (*WorkflowState, error) {
		gotMemory = memoryBySubtask["api"]
		return &WorkflowState{
			RunID:   plan.TaskID,
			Nodes:   map[string]NodeStatus{"development": NodeStatusCompleted},
			Details: map[string]any{"subtask_results": []*SubtaskResult{{Subtask: plan.Subtasks[0], Status: "completed"}}},
		}, nil
	}
	defer func() { runDevelopmentOnlyWithMemoryForNode = prevRun }()

	plan := &Plan{TaskID: "run-node-dev-brief", Subtasks: []Subtask{validSubtask("api")}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal(plan) error = %v", err)
	}
	result, err := (&developmentNode{}).Execute(context.Background(), NodeRequest{
		RunID:    plan.TaskID,
		RepoRoot: t.TempDir(),
		Spec:     NodeSpec{ID: "development", Kind: NodeKindDevelopment},
		Inputs:   NodeInputs{"planning": {Values: map[string]any{"plan": json.RawMessage(planJSON)}}},
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
	if !strings.Contains(gotMemory, "mem_node_dev") {
		t.Fatalf("memoryBlock = %q, want injected ID", gotMemory)
	}
}
