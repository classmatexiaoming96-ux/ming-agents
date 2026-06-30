package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	if result.FailureClass != FailureClassProductDefect {
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
	if decoded.RunID != runID || decoded.FailureClass != FailureClassProductDefect {
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
	if result.FailureClass != FailureClassNone {
		t.Fatalf("FailureClass = %q, want none", result.FailureClass)
	}
}

func TestClassifyFailureDistinguishesEnvironmentAndValidatorIssues(t *testing.T) {
	if got := classifyFailure([]TestResult{{ExitCode: -1}}); got != FailureClassValidatorIssue {
		t.Fatalf("classifyFailure(exit -1) = %q, want validator_issue", got)
	}
	if got := classifyFailure([]TestResult{{ExitCode: 127, StderrPath: "go: not found"}}); got != FailureClassEnvironmentBlock {
		t.Fatalf("classifyFailure(missing command) = %q, want environment_block", got)
	}
	if got := classifyFailure([]TestResult{{ExitCode: 1}}); got != FailureClassProductDefect {
		t.Fatalf("classifyFailure(exit 1) = %q, want product_defect", got)
	}
}

func TestEvaluationResultJSONRoundTrip(t *testing.T) {
	original := EvaluationResult{
		RunID:        "run-json",
		TestResults:  []TestResult{{TestID: "subtask-api", Command: "go test ./...", ExitCode: 0, Passed: true}},
		Evidence:     []EvidenceRef{{Type: "test_log", Path: "test.log"}},
		FailureClass: FailureClassNone,
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

func TestEvaluationResultJSONRoundTripWithSubtaskResults(t *testing.T) {
	original := EvaluationResult{
		RunID:        "run-1",
		Passed:       false,
		FailureClass: FailureClassProductDefect,
		TestResults: []TestResult{
			{TestID: "subtask-api", SubtaskID: "api", ExitCode: 1, Passed: false, FailureClass: FailureClassProductDefect},
		},
		SubtaskResults: []SubtaskFailure{
			{
				SubtaskID:    "api",
				FailureClass: FailureClassProductDefect,
				Reason:       "test failed: expected 200, got 500",
				RetryAdvice:  "check server error in api handler",
				NextAction:   NextActionRetrySubtask,
			},
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got EvaluationResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(got.SubtaskResults) != 1 {
		t.Fatalf("len(SubtaskResults) = %d, want 1", len(got.SubtaskResults))
	}
	if got.SubtaskResults[0].SubtaskID != "api" {
		t.Fatalf("SubtaskID = %q, want api", got.SubtaskResults[0].SubtaskID)
	}
	if got.SubtaskResults[0].FailureClass != FailureClassProductDefect {
		t.Fatalf("FailureClass = %q, want %q", got.SubtaskResults[0].FailureClass, FailureClassProductDefect)
	}
	if got.TestResults[0].FailureClass != FailureClassProductDefect {
		t.Fatalf("TestResult FailureClass = %q, want product_defect", got.TestResults[0].FailureClass)
	}
}

func TestOldEvaluationResultJSONUnmarshalsBackwardCompatible(t *testing.T) {
	oldJSON := `{
		"run_id": "run-1",
		"passed": true,
		"test_results": []
	}`
	var got EvaluationResult
	if err := json.Unmarshal([]byte(oldJSON), &got); err != nil {
		t.Fatalf("old EvaluationResult JSON should unmarshal: %v", err)
	}
	if len(got.SubtaskResults) != 0 {
		t.Fatalf("len(SubtaskResults) = %d, want 0", len(got.SubtaskResults))
	}
}

func TestSubtaskPlannedFilesBackwardCompatible(t *testing.T) {
	oldJSON := `{"id": "api", "description": "build API"}`
	var got Subtask
	if err := json.Unmarshal([]byte(oldJSON), &got); err != nil {
		t.Fatalf("old Subtask JSON should unmarshal: %v", err)
	}
	if len(got.PlannedFiles) != 0 {
		t.Fatalf("len(PlannedFiles) = %d, want 0", len(got.PlannedFiles))
	}
}

func TestFailureClassFieldsUseStrongType(t *testing.T) {
	fields := []struct {
		typ  reflect.Type
		name string
	}{
		{reflect.TypeOf(EvaluationResult{}), "FailureClass"},
		{reflect.TypeOf(TestResult{}), "FailureClass"},
		{reflect.TypeOf(ReviewIssue{}), "FailureClass"},
	}
	want := reflect.TypeOf(FailureClass(""))
	for _, field := range fields {
		got, ok := field.typ.FieldByName(field.name)
		if !ok {
			t.Fatalf("%s missing field %s", field.typ.Name(), field.name)
		}
		if got.Type != want {
			t.Fatalf("%s.%s type = %v, want %v", field.typ.Name(), field.name, got.Type, want)
		}
	}
}

func TestNextActionFieldsUseStrongType(t *testing.T) {
	fields := []struct {
		typ  reflect.Type
		name string
	}{
		{reflect.TypeOf(AttemptEvent{}), "NextAction"},
		{reflect.TypeOf(SubtaskFailure{}), "NextAction"},
	}
	want := reflect.TypeOf(NextAction(""))
	for _, field := range fields {
		got, ok := field.typ.FieldByName(field.name)
		if !ok {
			t.Fatalf("%s missing field %s", field.typ.Name(), field.name)
		}
		if got.Type != want {
			t.Fatalf("%s.%s type = %v, want %v", field.typ.Name(), field.name, got.Type, want)
		}
	}
	actions := []NextAction{
		NextActionRetrySubtask,
		NextActionFixEnvironment,
		NextActionAskUser,
		NextActionRegenerateSubtask,
		NextActionRetryReport,
		NextActionRetryGenerator,
	}
	if len(actions) != 6 {
		t.Fatalf("len(actions) = %d, want 6", len(actions))
	}
}

func TestRunEvaluationPopulatesSubtaskFailureAttribution(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-attribution"
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	subtaskDir := filepath.Join(runDir, "subtask-api")
	if err := os.MkdirAll(subtaskDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(subtaskDir, "test_command.txt"), []byte("printf 'unit failed'; exit 1"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "test.log"), []byte("test evidence"), 0644); err != nil {
		t.Fatalf("WriteFile(test.log) error = %v", err)
	}

	result, err := RunEvaluation(context.Background(), repoRoot, runID)
	if err != nil {
		t.Fatalf("RunEvaluation() error = %v", err)
	}
	if len(result.TestResults) != 1 {
		t.Fatalf("len(TestResults) = %d, want 1", len(result.TestResults))
	}
	if result.TestResults[0].FailureClass != FailureClassProductDefect {
		t.Fatalf("TestResult FailureClass = %q, want %q", result.TestResults[0].FailureClass, FailureClassProductDefect)
	}
	if len(result.SubtaskResults) != 1 {
		t.Fatalf("len(SubtaskResults) = %d, want 1", len(result.SubtaskResults))
	}
	got := result.SubtaskResults[0]
	if got.SubtaskID != "subtask-api" {
		t.Fatalf("SubtaskFailure SubtaskID = %q, want subtask-api", got.SubtaskID)
	}
	if got.FailureClass != FailureClassProductDefect {
		t.Fatalf("SubtaskFailure FailureClass = %q, want %q", got.FailureClass, FailureClassProductDefect)
	}
	if got.RetryAdvice == "" || got.NextAction == "" {
		t.Fatalf("SubtaskFailure missing retry routing: %+v", got)
	}
	if len(got.EvidenceRefs) != 1 || got.EvidenceRefs[0].Type != "test_log" {
		t.Fatalf("SubtaskFailure EvidenceRefs = %+v, want test_log evidence", got.EvidenceRefs)
	}
}
