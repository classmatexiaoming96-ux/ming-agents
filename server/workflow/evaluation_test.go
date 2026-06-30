package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestEvaluationNodeImplementsRollbackCapableNode(t *testing.T) {
	var _ RollbackCapableNode = (*evaluationNode)(nil)
}

func TestEvaluationNodePrepareRollback(t *testing.T) {
	node := &evaluationNode{}
	decision, err := node.PrepareRollback(context.Background(), RollbackContext{
		RunID:    "run-1",
		NodeID:   "evaluation",
		NodeKind: NodeKindEvaluation,
		Unit:     RollbackUnit{Scope: "command:subtask-api", MaxAttempts: 2, ReusePolicy: SessionReuseNewSession},
	}, RollbackSignal{FailureClass: FailureClassTransient, Reason: "flaky command"})
	if err != nil {
		t.Fatalf("PrepareRollback() error = %v", err)
	}
	if decision.Action != RollbackActionRetryReport {
		t.Fatalf("Action = %q, want %q", decision.Action, RollbackActionRetryReport)
	}
	if decision.ReuseSession {
		t.Fatal("ReuseSession = true, want false")
	}
}

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
	if len(result.Evidence) != 1 || result.Evidence[0].Type != EvidenceTypeTestLog {
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

func TestRunEvaluationWritesCommandAttemptLineage(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-eval-lineage"
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	subtaskDir := filepath.Join(runDir, "subtask-api")
	if err := os.MkdirAll(subtaskDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(subtaskDir, "test_command.txt"), []byte("printf 'ok'"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := RunEvaluation(context.Background(), repoRoot, runID); err != nil {
		t.Fatalf("RunEvaluation() error = %v", err)
	}
	events, err := ReadAttemptEvents(repoRoot, runID, "evaluation")
	if err != nil {
		t.Fatalf("ReadAttemptEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	event := events[0]
	if event.Scope != "command:subtask-api" {
		t.Fatalf("Scope = %q, want command:subtask-api", event.Scope)
	}
	if event.Outcome == nil || !event.Outcome.Passed {
		t.Fatalf("Outcome = %#v, want passed outcome", event.Outcome)
	}
}

func TestRunEvaluationDoesNotRetryProductDefect(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-eval-no-product-retry"
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	subtaskDir := filepath.Join(runDir, "subtask-api")
	if err := os.MkdirAll(subtaskDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(subtaskDir, "test_command.txt"), []byte("printf 'unit failed'; exit 1"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := RunEvaluation(context.Background(), repoRoot, runID); err != nil {
		t.Fatalf("RunEvaluation() error = %v", err)
	}
	events, err := ReadAttemptEvents(repoRoot, runID, "evaluation")
	if err != nil {
		t.Fatalf("ReadAttemptEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want product defect to stop after one attempt", len(events))
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
	if got := classifyFailure([]TestResult{{ExitCode: -1, FailureClass: FailureClassValidatorIssue}}); got != FailureClassValidatorIssue {
		t.Fatalf("classifyFailure(exit -1) = %q, want validator_issue", got)
	}
	if got := classifyFailure([]TestResult{{ExitCode: 127, FailureClass: FailureClassEnvironmentBlock}}); got != FailureClassEnvironmentBlock {
		t.Fatalf("classifyFailure(missing command) = %q, want environment_block", got)
	}
	if got := classifyFailure([]TestResult{{ExitCode: 1, FailureClass: FailureClassEnvironmentBlock, StderrPath: "/tmp/subtask-api_stderr.txt"}}); got != FailureClassEnvironmentBlock {
		t.Fatalf("classifyFailure(exit 1 env class) = %q, want environment_block", got)
	}
	if got := classifyFailure([]TestResult{{ExitCode: 1, FailureClass: FailureClassProductDefect}}); got != FailureClassProductDefect {
		t.Fatalf("classifyFailure(exit 1) = %q, want product_defect", got)
	}
}

func TestEvaluationResultJSONRoundTrip(t *testing.T) {
	original := EvaluationResult{
		RunID:        "run-json",
		TestResults:  []TestResult{{TestID: "subtask-api", Command: "go test ./...", ExitCode: 0, Passed: true}},
		Evidence:     []EvidenceRef{{Type: EvidenceTypeTestLog, Path: "test.log"}},
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
	if decoded.RunID != original.RunID || decoded.Evidence[0].Type != EvidenceTypeTestLog {
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
	if len(got.EvidenceRefs) != 1 || got.EvidenceRefs[0].Type != EvidenceTypeTestLog {
		t.Fatalf("SubtaskFailure EvidenceRefs = %+v, want test_log evidence", got.EvidenceRefs)
	}
}

func TestAttributeFailureToSubtaskUsesPlannedFilesBeforeFallbacks(t *testing.T) {
	plan := &Plan{TaskID: "run-attribution", Subtasks: []Subtask{
		{ID: "api", RepoPath: "server/api", PlannedFiles: []string{"server/api/handler.go"}},
		{ID: "ui", RepoPath: "web", PlannedFiles: []string{"web/app.ts"}},
	}}

	got := AttributeFailureToSubtask(plan, []string{"server/api/handler.go"}, TestResult{SubtaskID: "ui"})
	if got != "api" {
		t.Fatalf("AttributeFailureToSubtask() = %q, want api", got)
	}
}

func TestAttributeFailureToSubtaskUsesRepoPathWhenNoPlannedFileMatches(t *testing.T) {
	plan := &Plan{TaskID: "run-attribution", Subtasks: []Subtask{
		{ID: "api", RepoPath: "server/api"},
		{ID: "ui", RepoPath: "web"},
	}}

	got := AttributeFailureToSubtask(plan, []string{"web/components/button.tsx"}, TestResult{SubtaskID: "api"})
	if got != "ui" {
		t.Fatalf("AttributeFailureToSubtask() = %q, want ui", got)
	}
}

func TestAttributeFailureToSubtaskFallsBackToTestResultSubtaskID(t *testing.T) {
	plan := &Plan{TaskID: "run-attribution", Subtasks: []Subtask{
		{ID: "api", RepoPath: "server/api"},
	}}

	got := AttributeFailureToSubtask(plan, []string{"docs/readme.md"}, TestResult{SubtaskID: "subtask-api"})
	if got != "subtask-api" {
		t.Fatalf("AttributeFailureToSubtask() = %q, want subtask-api", got)
	}
}

func TestAttributeFailureToSubtaskReturnsEmptyForAmbiguousSharedFile(t *testing.T) {
	plan := &Plan{TaskID: "run-attribution", Subtasks: []Subtask{
		{ID: "api", RepoPath: "server", PlannedFiles: []string{"server/shared/config.go"}},
		{ID: "worker", RepoPath: "server", PlannedFiles: []string{"server/shared/config.go"}},
	}}

	got := AttributeFailureToSubtask(plan, []string{"server/shared/config.go"}, TestResult{SubtaskID: "api"})
	if got != "" {
		t.Fatalf("AttributeFailureToSubtask() = %q, want empty for ambiguous planned file", got)
	}
}

func TestChangedFilesReturnsUnstagedTrackedFile(t *testing.T) {
	repoRoot := initTempGitRepo(t)
	writeFile(t, repoRoot, "pkg/feature/file.go", "package feature\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "initial")

	writeFile(t, repoRoot, "pkg/feature/file.go", "package feature\n\nfunc Changed() {}\n")

	got, err := ChangedFiles(repoRoot)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	want := []string{"pkg/feature/file.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedFiles() = %#v, want %#v", got, want)
	}
}

func TestChangedFilesReturnsStagedFile(t *testing.T) {
	repoRoot := initTempGitRepo(t)
	writeFile(t, repoRoot, "cmd/app/main.go", "package main\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "initial")

	writeFile(t, repoRoot, "cmd/app/main.go", "package main\n\nfunc main() {}\n")
	git(t, repoRoot, "add", "cmd/app/main.go")

	got, err := ChangedFiles(repoRoot)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	want := []string{"cmd/app/main.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedFiles() = %#v, want %#v", got, want)
	}
}

func TestChangedFilesSortsAndDedupesStagedAndUnstaged(t *testing.T) {
	repoRoot := initTempGitRepo(t)
	writeFile(t, repoRoot, "zeta.go", "package main\n")
	writeFile(t, repoRoot, "docs/readme.md", "# docs\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "initial")

	writeFile(t, repoRoot, "zeta.go", "package main\n\nfunc Staged() {}\n")
	writeFile(t, repoRoot, "docs/readme.md", "# changed\n")
	git(t, repoRoot, "add", "zeta.go", "docs/readme.md")
	writeFile(t, repoRoot, "zeta.go", "package main\n\nfunc Staged() {}\nfunc Unstaged() {}\n")

	got, err := ChangedFiles(repoRoot)
	if err != nil {
		t.Fatalf("ChangedFiles() error = %v", err)
	}
	want := []string{"docs/readme.md", "zeta.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedFiles() = %#v, want sorted deduped %#v", got, want)
	}
}

func TestChangedFilesNonGitRepoReturnsClassifiedError(t *testing.T) {
	_, err := ChangedFiles(t.TempDir())
	if err == nil {
		t.Fatal("ChangedFiles() error = nil, want classified error")
	}
	var classified interface {
		FailureClass() FailureClass
	}
	if !errors.As(err, &classified) {
		t.Fatalf("ChangedFiles() error %T does not expose FailureClass()", err)
	}
	if classified.FailureClass() != FailureClassEnvironmentBlock {
		t.Fatalf("FailureClass() = %q, want %q", classified.FailureClass(), FailureClassEnvironmentBlock)
	}
}

func TestEnsureGitRepoWrapsNonGitRepoAsEnvironmentBlock(t *testing.T) {
	err := ensureGitRepo(t.TempDir())
	if err == nil {
		t.Fatal("ensureGitRepo() error = nil, want classified error")
	}
	if !strings.Contains(err.Error(), "ensure git repo") {
		t.Fatalf("ensureGitRepo() error = %q, want ensure git repo context", err.Error())
	}
	var classified interface {
		FailureClass() FailureClass
	}
	if !errors.As(err, &classified) {
		t.Fatalf("ensureGitRepo() error %T does not expose FailureClass()", err)
	}
	if classified.FailureClass() != FailureClassEnvironmentBlock {
		t.Fatalf("FailureClass() = %q, want %q", classified.FailureClass(), FailureClassEnvironmentBlock)
	}
}

func TestEnsureGitRepoRejectsDirectoryInsideAnotherWorktree(t *testing.T) {
	repoRoot := initTempGitRepo(t)
	child := filepath.Join(repoRoot, "nested")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatalf("MkdirAll(child) error = %v", err)
	}

	err := ensureGitRepo(child)
	if err == nil {
		t.Fatal("ensureGitRepo(child) error = nil, want classified error")
	}
	if !strings.Contains(err.Error(), "does not match git top-level") {
		t.Fatalf("ensureGitRepo(child) error = %q, want top-level mismatch context", err.Error())
	}
	var classified interface {
		FailureClass() FailureClass
	}
	if !errors.As(err, &classified) {
		t.Fatalf("ensureGitRepo(child) error %T does not expose FailureClass()", err)
	}
	if classified.FailureClass() != FailureClassEnvironmentBlock {
		t.Fatalf("FailureClass() = %q, want %q", classified.FailureClass(), FailureClassEnvironmentBlock)
	}
}

func TestChangedFilesHaveGoCodeChanges(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want bool
	}{
		{name: "docs only", in: []string{"docs/output.md", "README.md"}, want: false},
		{name: "no go code", in: []string{"scripts/setup.sh", "web/app.ts"}, want: false},
		{name: "go code", in: []string{"server/workflow/evaluation.go"}, want: true},
		{name: "go test", in: []string{"server/workflow/evaluation_test.go"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := changedFilesHaveGoCode(tt.in); got != tt.want {
				t.Fatalf("changedFilesHaveGoCode(%#v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRunCoverageCommandParsesFullCoverageAndWritesProfile(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-coverage-full"
	fakeGo := installFakeGo(t, repoRoot, `#!/bin/sh
set -eu
if [ "$1" = "test" ]; then
	profile=""
	for arg in "$@"; do
		case "$arg" in
			-coverprofile=*) profile="${arg#-coverprofile=}" ;;
		esac
	done
	mkdir -p "$(dirname "$profile")"
	printf 'mode: set\n' > "$profile"
	pwd > "$profile.cwd"
	printf 'ok example.test 0.001s coverage: 100.0%% of statements\n'
	exit 0
fi
if [ "$1" = "tool" ] && [ "$2" = "cover" ]; then
	printf 'example/file.go:1: Function 100.0%%\n'
	printf 'total: (statements) 100.0%%\n'
	exit 0
fi
printf 'unexpected go args: %s\n' "$*" >&2
exit 2
`)
	t.Setenv("PATH", fakeGo+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := RunCoverageCommand(context.Background(), repoRoot, runID)
	if err != nil {
		t.Fatalf("RunCoverageCommand() error = %v", err)
	}
	wantPath := filepath.Join(repoRoot, ".workflow", "runs", runID, "coverage.out")
	if result.CoveragePath != wantPath {
		t.Fatalf("CoveragePath = %q, want %q", result.CoveragePath, wantPath)
	}
	if result.TotalPercent != 100.0 {
		t.Fatalf("TotalPercent = %v, want 100.0", result.TotalPercent)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("coverage.out was not written at %s: %v", wantPath, err)
	}
	cwd, err := os.ReadFile(wantPath + ".cwd")
	if err != nil {
		t.Fatalf("ReadFile(coverage cwd) error = %v", err)
	}
	if strings.TrimSpace(string(cwd)) != repoRoot {
		t.Fatalf("go test ran in %q, want repoRoot %q", strings.TrimSpace(string(cwd)), repoRoot)
	}
}

func TestRunCoverageCommandParsesBelowFullCoverage(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-coverage-partial"
	fakeGo := installFakeGo(t, repoRoot, `#!/bin/sh
set -eu
if [ "$1" = "test" ]; then
	profile=""
	for arg in "$@"; do
		case "$arg" in
			-coverprofile=*) profile="${arg#-coverprofile=}" ;;
		esac
	done
	mkdir -p "$(dirname "$profile")"
	printf 'mode: set\n' > "$profile"
	exit 0
fi
if [ "$1" = "tool" ] && [ "$2" = "cover" ]; then
	printf 'total: (statements) 87.5%%\n'
	exit 0
fi
exit 2
`)
	t.Setenv("PATH", fakeGo+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := RunCoverageCommand(context.Background(), repoRoot, runID)
	if err != nil {
		t.Fatalf("RunCoverageCommand() error = %v", err)
	}
	if result.TotalPercent != 87.5 {
		t.Fatalf("TotalPercent = %v, want 87.5", result.TotalPercent)
	}
}

func TestRunCoverageCommandGoTestFailureIsClassified(t *testing.T) {
	repoRoot := t.TempDir()
	fakeGo := installFakeGo(t, repoRoot, `#!/bin/sh
if [ "$1" = "test" ]; then
	printf 'go: module download failed: network unreachable\n' >&2
	exit 1
fi
exit 2
`)
	t.Setenv("PATH", fakeGo+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := RunCoverageCommand(context.Background(), repoRoot, "run-coverage-go-failure")
	assertClassifiedEvaluationError(t, err, FailureClassEnvironmentBlock)
}

func TestRunCoverageCommandCoverToolFailureIsClassified(t *testing.T) {
	repoRoot := t.TempDir()
	fakeGo := installFakeGo(t, repoRoot, `#!/bin/sh
set -eu
if [ "$1" = "test" ]; then
	profile=""
	for arg in "$@"; do
		case "$arg" in
			-coverprofile=*) profile="${arg#-coverprofile=}" ;;
		esac
	done
	mkdir -p "$(dirname "$profile")"
	printf 'mode: set\n' > "$profile"
	exit 0
fi
if [ "$1" = "tool" ] && [ "$2" = "cover" ]; then
	printf 'cover: malformed coverage profile\n' >&2
	exit 1
fi
exit 2
`)
	t.Setenv("PATH", fakeGo+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := RunCoverageCommand(context.Background(), repoRoot, "run-coverage-tool-failure")
	assertClassifiedEvaluationError(t, err, FailureClassValidatorIssue)
}

func TestRunEvaluationFailsCoverageGateForChangedGoCode(t *testing.T) {
	repoRoot := initTempGitRepo(t)
	writeFile(t, repoRoot, "pkg/example/example.go", "package example\n\nfunc Covered() int { return 1 }\n")
	writeFile(t, repoRoot, "go.mod", "module example.test\n\ngo 1.22\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "initial")
	writeFile(t, repoRoot, "pkg/example/example.go", "package example\n\nfunc Covered() int { return 1 }\nfunc Uncovered() int { return 2 }\n")
	fakeGo := installFakeGo(t, repoRoot, `#!/bin/sh
set -eu
if [ "$1" = "test" ]; then
	profile=""
	for arg in "$@"; do
		case "$arg" in
			-coverprofile=*) profile="${arg#-coverprofile=}" ;;
		esac
	done
	mkdir -p "$(dirname "$profile")"
	printf 'mode: set\n' > "$profile"
	exit 0
fi
if [ "$1" = "tool" ] && [ "$2" = "cover" ]; then
	printf 'total: (statements) 99.9%%\n'
	exit 0
fi
exit 2
`)
	t.Setenv("PATH", fakeGo+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := RunEvaluation(context.Background(), repoRoot, "run-coverage-gate")
	if err != nil {
		t.Fatalf("RunEvaluation() error = %v", err)
	}
	if result.Passed {
		t.Fatal("RunEvaluation() Passed = true, want coverage gate failure")
	}
	if result.FailureClass != FailureClassProductDefect {
		t.Fatalf("FailureClass = %q, want %q", result.FailureClass, FailureClassProductDefect)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].Type != EvidenceTypeCoverage {
		t.Fatalf("Evidence = %#v, want coverage evidence", result.Evidence)
	}
	if len(result.TestResults) != 1 || result.TestResults[0].TestID != "coverage" || result.TestResults[0].Passed {
		t.Fatalf("TestResults = %#v, want failing coverage test result", result.TestResults)
	}
}

func TestRunEvaluationAttributesCoverageFailureToMatchingSubtask(t *testing.T) {
	repoRoot := initTempGitRepo(t)
	writeFile(t, repoRoot, "pkg/api/api.go", "package api\n\nfunc Covered() int { return 1 }\n")
	writeFile(t, repoRoot, "go.mod", "module example.test\n\ngo 1.22\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "initial")
	writeFile(t, repoRoot, "pkg/api/api.go", "package api\n\nfunc Covered() int { return 1 }\nfunc Uncovered() int { return 2 }\n")
	fakeGo := installFakeGo(t, repoRoot, `#!/bin/sh
set -eu
if [ "$1" = "test" ]; then
	profile=""
	for arg in "$@"; do
		case "$arg" in
			-coverprofile=*) profile="${arg#-coverprofile=}" ;;
		esac
	done
	mkdir -p "$(dirname "$profile")"
	printf 'mode: set\n' > "$profile"
	exit 0
fi
if [ "$1" = "tool" ] && [ "$2" = "cover" ]; then
	printf 'total: (statements) 99.9%%\n'
	exit 0
fi
exit 2
`)
	t.Setenv("PATH", fakeGo+string(os.PathListSeparator)+os.Getenv("PATH"))
	plan := &Plan{TaskID: "run-coverage-attribution", Subtasks: []Subtask{
		{ID: "api", RepoPath: "pkg/api", PlannedFiles: []string{"pkg/api/api.go"}},
		{ID: "web", RepoPath: "web"},
	}}

	result, err := RunEvaluationWithPlan(context.Background(), repoRoot, plan.TaskID, plan)
	if err != nil {
		t.Fatalf("RunEvaluationWithPlan() error = %v", err)
	}
	if len(result.TestResults) != 1 || result.TestResults[0].SubtaskID != "api" {
		t.Fatalf("coverage TestResults = %#v, want subtask api", result.TestResults)
	}
	if len(result.SubtaskResults) != 1 || result.SubtaskResults[0].SubtaskID != "api" {
		t.Fatalf("SubtaskResults = %#v, want coverage failure attributed to api", result.SubtaskResults)
	}
}

func TestRunEvaluationSkipsCoverageGateForDocsOnlyChange(t *testing.T) {
	repoRoot := initTempGitRepo(t)
	writeFile(t, repoRoot, "docs/readme.md", "# docs\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "initial")
	writeFile(t, repoRoot, "docs/readme.md", "# changed\n")
	fakeGo := installFakeGo(t, repoRoot, `#!/bin/sh
printf 'coverage gate should not run for docs-only changes\n' >&2
exit 7
`)
	t.Setenv("PATH", fakeGo+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := RunEvaluation(context.Background(), repoRoot, "run-docs-only")
	if err != nil {
		t.Fatalf("RunEvaluation() error = %v", err)
	}
	if !result.Passed {
		t.Fatalf("RunEvaluation() Passed = false, want docs-only coverage skip: %#v", result)
	}
	for _, evidence := range result.Evidence {
		if evidence.Type == EvidenceTypeCoverage {
			t.Fatalf("Evidence contains coverage for docs-only change: %#v", result.Evidence)
		}
	}
}

func initTempGitRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	git(t, repoRoot, "init")
	git(t, repoRoot, "config", "user.email", "test@example.com")
	git(t, repoRoot, "config", "user.name", "Test User")
	return repoRoot
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, repoRoot, relPath, contents string) {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func installFakeGo(t *testing.T, repoRoot, script string) string {
	t.Helper()
	binDir := filepath.Join(repoRoot, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll(fake go bin) error = %v", err)
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.bat"
	}
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile(fake go) error = %v", err)
	}
	return binDir
}

func assertClassifiedEvaluationError(t *testing.T, err error, want FailureClass) {
	t.Helper()
	if err == nil {
		t.Fatal("RunCoverageCommand() error = nil, want classified error")
	}
	var classified interface {
		FailureClass() FailureClass
	}
	if !errors.As(err, &classified) {
		t.Fatalf("RunCoverageCommand() error %T does not expose FailureClass()", err)
	}
	if classified.FailureClass() != want {
		t.Fatalf("FailureClass() = %q, want %q; err = %v", classified.FailureClass(), want, err)
	}
}
