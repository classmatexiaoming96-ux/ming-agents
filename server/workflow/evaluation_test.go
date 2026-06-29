package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunEvaluationWritesEvaluationJSONAndClassifiesFailure(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-eval"
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	subtaskDir := filepath.Join(runDir, "subtask-api")
	if err := os.MkdirAll(subtaskDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writePhaseStatusAt(repoRoot, runID, &PhaseStatus{Phase: "evaluation", GateStatus: "waiting_user"}); err != nil {
		t.Fatalf("writePhaseStatusAt() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(subtaskDir, "test_command.txt"), []byte("printf 'unit failed'; exit 1"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "test.log"), []byte("test evidence"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result, err := RunEvaluation(context.Background(), repoRoot, runID)
	if err != nil {
		t.Fatalf("RunEvaluation() error = %v", err)
	}
	if result.Passed {
		t.Fatal("RunEvaluation() Passed = true, want false")
	}
	if result.FailureClass != "product_defect" {
		t.Fatalf("FailureClass = %q, want product_defect", result.FailureClass)
	}
	if len(result.TestResults) != 1 {
		t.Fatalf("len(TestResults) = %d, want 1", len(result.TestResults))
	}
	if result.TestResults[0].StdoutPath == "" {
		t.Fatalf("StdoutPath empty in result: %+v", result.TestResults[0])
	}
	if len(result.Evidence) != 1 || result.Evidence[0].Type != "test_log" {
		t.Fatalf("Evidence = %+v, want test_log", result.Evidence)
	}

	data, err := os.ReadFile(filepath.Join(runDir, "evaluation.json"))
	if err != nil {
		t.Fatalf("ReadFile(evaluation.json) error = %v", err)
	}
	var decoded EvaluationResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal(evaluation.json) error = %v", err)
	}
	if decoded.RunID != runID || decoded.FailureClass != "product_defect" {
		t.Fatalf("decoded evaluation = %+v", decoded)
	}
}

func TestRunEvaluationPassesWhenNoSubtasks(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-no-subtasks"
	if err := writePhaseStatusAt(repoRoot, runID, &PhaseStatus{Phase: "evaluation", GateStatus: "passed"}); err != nil {
		t.Fatalf("writePhaseStatusAt() error = %v", err)
	}

	result, err := RunEvaluation(context.Background(), repoRoot, runID)
	if err != nil {
		t.Fatalf("RunEvaluation() error = %v", err)
	}
	if !result.Passed {
		t.Fatalf("RunEvaluation() Passed = false, result = %+v", result)
	}
	if result.FailureClass != "none" {
		t.Fatalf("FailureClass = %q, want none", result.FailureClass)
	}
}

func TestClassifyFailureDistinguishesEnvironmentAndValidatorIssues(t *testing.T) {
	if got := classifyFailure([]TestResult{{ExitCode: -1}}); got != "validator_issue" {
		t.Fatalf("classifyFailure(exit -1) = %q, want validator_issue", got)
	}
	if got := classifyFailure([]TestResult{{ExitCode: 127, StderrPath: "go: not found"}}); got != "environment_block" {
		t.Fatalf("classifyFailure(missing command) = %q, want environment_block", got)
	}
	if got := classifyFailure([]TestResult{{ExitCode: 1}}); got != "product_defect" {
		t.Fatalf("classifyFailure(exit 1) = %q, want product_defect", got)
	}
}

func TestEvaluationResultJSONRoundTrip(t *testing.T) {
	original := EvaluationResult{
		RunID:        "run-json",
		TestResults:  []TestResult{{TestID: "subtask-api", Command: "go test ./...", ExitCode: 0, Passed: true}},
		Evidence:     []EvidenceRef{{Type: "test_log", Path: "test.log"}},
		FailureClass: "none",
		Passed:       true,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"test_results"`) {
		t.Fatalf("marshaled JSON missing test_results: %s", data)
	}
	var decoded EvaluationResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.RunID != original.RunID || decoded.Evidence[0].Type != "test_log" {
		t.Fatalf("decoded = %+v", decoded)
	}
}
