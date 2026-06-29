package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerificationRunnerRunsIndependentCodexSession(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-verify"
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "test.log"), []byte("test log"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	startsPath := filepath.Join(repoRoot, "starts.txt")
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'start\n' >> starts.txt
printf 'OpenAI Codex\n› '
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'VERDICT: PASS\n'
      printf 'REASON: artifacts satisfy acceptance criteria\n'
      printf '%s\n' "$marker"
      ;;
  esac
done
`)
	manager := NewCodexSessionManager(CodexConfig{
		Command:        cmd,
		StartupTimeout: time.Second,
		ReadyTimeout:   time.Second,
		InvokeTimeout:  time.Second,
	})
	runner := NewVerificationRunner(manager)

	result, err := runner.Run(context.Background(), runID, VerificationPlan{
		TaskID: runID,
		Subtasks: []VerificationSubtask{{
			ID:                 "api",
			Description:        "implement api",
			AcceptanceCriteria: []string{"tests pass"},
		}},
	}, ArtifactIndex{
		PlanPath:    filepath.Join(repoRoot, "docs", "planning.md"),
		OutputPath:  filepath.Join(repoRoot, "docs", "output.md"),
		EvidenceDir: runDir,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Verdict != "PASS" {
		t.Fatalf("result = %+v, want raw PASS verdict", result)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].Type != "test_log" {
		t.Fatalf("evidence = %+v, want test_log", result.Evidence)
	}
	if _, err := os.Stat(filepath.Join(runDir, "evaluator_prompt.md")); err != nil {
		t.Fatalf("evaluator prompt not written: %v", err)
	}
	starts, err := os.ReadFile(startsPath)
	if err != nil {
		t.Fatalf("read starts: %v", err)
	}
	if strings.Count(string(starts), "start") != 1 {
		t.Fatalf("starts = %q, want one independent evaluator session", starts)
	}
}

func TestVerificationRunnerParsesFailVerdict(t *testing.T) {
	runner := NewVerificationRunner(NewCodexSessionManager(CodexConfig{}))
	verdict := runner.parseVerdict("VERDICT: FAIL\nREASON: missing output\n")
	if verdict != "FAIL" {
		t.Fatalf("verdict = %q, want FAIL", verdict)
	}
}

func TestNewVerificationRunnerAcceptsCodexSessionManager(t *testing.T) {
	manager := NewCodexSessionManager(CodexConfig{})
	runner := NewVerificationRunner(manager)
	if runner == nil || runner.manager != manager {
		t.Fatalf("runner = %+v, want manager attached", runner)
	}
}
