package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDevelopmentNodeSkipsInternalReviewAndEvaluation(t *testing.T) {
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
		Config:   map[string]any{ConfigSkipInternalReviewEvaluation: true},
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

	runDir := filepath.Join(repoRoot, ".workflow", "runs", plan.TaskID)
	if _, err := os.Stat(filepath.Join(runDir, "development", "review.out.md")); !os.IsNotExist(err) {
		t.Fatalf("development node wrote review output, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "evaluation.json")); !os.IsNotExist(err) {
		t.Fatalf("development node wrote evaluation output, err=%v", err)
	}
}
