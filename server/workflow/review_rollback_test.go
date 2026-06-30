package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewSubtaskPathsUseSafeDirectoryAndReadableArtifacts(t *testing.T) {
	repoRoot := t.TempDir()
	paths := NewReviewSubtaskPaths(repoRoot, "run-1", "../api task")

	if paths.SubtaskID != "../api task" {
		t.Fatalf("SubtaskID = %q, want original id", paths.SubtaskID)
	}
	if strings.Contains(paths.SafeSubtaskID, "/") || strings.Contains(paths.SafeSubtaskID, " ") || strings.Contains(paths.SafeSubtaskID, "..") {
		t.Fatalf("SafeSubtaskID = %q, want path-safe id", paths.SafeSubtaskID)
	}
	wantDir := filepath.Join(repoRoot, ".workflow", "runs", "run-1", "review", "subtasks", paths.SafeSubtaskID)
	if paths.Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", paths.Dir, wantDir)
	}
	if filepath.Base(paths.PromptFile) != "review-"+paths.SafeSubtaskID+".prompt.md" {
		t.Fatalf("PromptFile = %q, want readable artifact name", paths.PromptFile)
	}
	if filepath.Base(paths.OutFile) != "review-"+paths.SafeSubtaskID+".out.md" {
		t.Fatalf("OutFile = %q, want readable artifact name", paths.OutFile)
	}
	if filepath.Base(paths.ExitFile) != "review-"+paths.SafeSubtaskID+".exit" {
		t.Fatalf("ExitFile = %q, want readable artifact name", paths.ExitFile)
	}
	if filepath.Base(paths.HistoryFile) != "review-"+paths.SafeSubtaskID+".messages.jsonl" {
		t.Fatalf("HistoryFile = %q, want readable history name", paths.HistoryFile)
	}
}

func TestReviewSubtaskSessionIDIncludesRunReviewAndSubtask(t *testing.T) {
	paths := NewReviewSubtaskPaths("/repo", "run-1", "api/task")

	if paths.SessionID != NewPTYSessionID("run-1", "review", "subtask-api/task", 1) {
		t.Fatalf("SessionID = %q, want review subtask pty session id", paths.SessionID)
	}
	if !strings.Contains(paths.SessionID, "run-1-review-subtask-api-task-1") {
		t.Fatalf("SessionID = %q, want run/review/subtask components", paths.SessionID)
	}
}

func TestReviewNodeImplementsRollbackCapableNode(t *testing.T) {
	var _ RollbackCapableNode = (*reviewNode)(nil)
}

func TestReviewNodeRunsSeparateReviewForEachSubtask(t *testing.T) {
	repoRoot := t.TempDir()
	plan := &Plan{
		TaskID: "run-review",
		Subtasks: []Subtask{
			{ID: "api", RepoPath: "server/api", Description: "api", PlannedFiles: []string{"server/api/api.go"}},
			{ID: "web ui", RepoPath: "web", Description: "web", PlannedFiles: []string{"web/app.ts"}},
		},
	}
	results := []*SubtaskResult{
		{Subtask: plan.Subtasks[0], SessionID: "session-api", Status: "completed", ExitCode: 0, OutFile: "/tmp/api.out.md"},
		{Subtask: plan.Subtasks[1], SessionID: "session-web", Status: "completed", ExitCode: 0, OutFile: "/tmp/web.out.md"},
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal(plan) error = %v", err)
	}
	state := &WorkflowState{
		RunID: plan.TaskID,
		Details: map[string]any{
			"subtask_results": results,
		},
	}

	oldRunner := runReviewCodexPrompt
	runReviewCodexPrompt = func(ctx context.Context, repoRoot, prompt string, timeout time.Duration) (string, error) {
		if strings.Contains(prompt, "subtask_id: api") {
			return "## Summary\napi passed\n\n## Issues\n", nil
		}
		if strings.Contains(prompt, "subtask_id: web ui") {
			return "## Summary\nweb passed\n\n## Issues\n", nil
		}
		t.Fatalf("unexpected prompt:\n%s", prompt)
		return "", nil
	}
	defer func() { runReviewCodexPrompt = oldRunner }()

	result, err := (&reviewNode{}).Execute(context.Background(), NodeRequest{
		RunID:    plan.TaskID,
		RepoRoot: repoRoot,
		Spec:     NodeSpec{ID: "review", Kind: NodeKindReview},
		Inputs: NodeInputs{
			"planning":    {NodeID: "planning", Values: map[string]any{"plan": json.RawMessage(planJSON)}},
			"development": {NodeID: "development", Values: map[string]any{"state": state}},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != NodeStatusCompleted {
		t.Fatalf("Status = %s, want %s; error=%s", result.Status, NodeStatusCompleted, result.Error)
	}
	report, _ := result.Values["report"].(*ReviewReport)
	if report == nil {
		t.Fatal("review node result missing report")
	}
	if len(report.SubtaskReports) != 2 {
		t.Fatalf("SubtaskReports len = %d, want 2", len(report.SubtaskReports))
	}
	for _, st := range plan.Subtasks {
		paths := NewReviewSubtaskPaths(repoRoot, plan.TaskID, st.ID)
		if _, err := os.Stat(paths.OutFile); err != nil {
			t.Fatalf("missing out artifact for %s at %s: %v", st.ID, paths.OutFile, err)
		}
		if _, err := os.Stat(paths.PromptFile); err != nil {
			t.Fatalf("missing prompt artifact for %s at %s: %v", st.ID, paths.PromptFile, err)
		}
	}

	events, err := ReadAttemptEvents(repoRoot, plan.TaskID, "review")
	if err != nil {
		t.Fatalf("ReadAttemptEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("attempt events = %d, want 2: %#v", len(events), events)
	}
	for _, event := range events {
		if !strings.HasPrefix(event.Scope, "review:subtask:") {
			t.Fatalf("attempt scope = %q, want review subtask scope", event.Scope)
		}
		if event.SubtaskID == "" {
			t.Fatalf("attempt missing SubtaskID: %#v", event)
		}
	}
}
