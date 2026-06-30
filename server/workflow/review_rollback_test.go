package workflow

import (
	"path/filepath"
	"strings"
	"testing"
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
