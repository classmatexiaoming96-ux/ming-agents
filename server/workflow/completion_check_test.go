package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePhaseStatusAndCheckCompletion(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	runID := "run-phase"
	if err := WritePhaseStatus(runID, &PhaseStatus{
		Phase:      "development",
		GateStatus: "passed",
		NextAction: "finish",
	}); err != nil {
		t.Fatalf("WritePhaseStatus() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(".workflow", "runs", runID, "phase_status.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var status PhaseStatus
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if status.RunID != runID || status.UpdatedAt.IsZero() {
		t.Fatalf("status missing run metadata: %+v", status)
	}

	if err := os.MkdirAll("docs", 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join("docs", "output.md"), []byte("# output\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	check, err := CheckCompletion(runID)
	if err != nil {
		t.Fatalf("CheckCompletion() error = %v", err)
	}
	if !check.Passed {
		t.Fatalf("CheckCompletion() Passed = false, missing = %v", check.Missing)
	}
	if len(check.EvidenceIndex) == 0 || check.EvidenceIndex[0].SubtaskID != "output" {
		t.Fatalf("EvidenceIndex = %+v, want output evidence", check.EvidenceIndex)
	}
}

func TestCheckCompletionReportsMissingPhaseStatus(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	check, err := CheckCompletion("missing-run")
	if err != nil {
		t.Fatalf("CheckCompletion() error = %v", err)
	}
	if check.Passed {
		t.Fatal("CheckCompletion() Passed = true, want false")
	}
	if len(check.Missing) != 1 || check.Missing[0] != "phase_status.json not found" {
		t.Fatalf("Missing = %#v", check.Missing)
	}
}

func TestCompletionCheckRecognizesCoverageEvidence(t *testing.T) {
	dir := t.TempDir()
	runID := "run-coverage"
	writeCompletionCheckBaseFiles(t, dir, runID)
	coveragePath := filepath.Join(dir, ".workflow", "runs", runID, "coverage.out")
	if err := os.WriteFile(coveragePath, []byte("mode: set\n"), 0644); err != nil {
		t.Fatalf("WriteFile(coverage.out) error = %v", err)
	}

	check, err := checkCompletionAt(dir, runID)
	if err != nil {
		t.Fatalf("checkCompletionAt() error = %v", err)
	}
	if !check.Passed {
		t.Fatalf("checkCompletionAt() Passed = false, missing = %v", check.Missing)
	}
	assertEvidenceType(t, check, "coverage", coveragePath)
}

func TestCompletionCheckRecognizesReviewReportEvidence(t *testing.T) {
	dir := t.TempDir()
	runID := "run-review-report"
	writeCompletionCheckBaseFiles(t, dir, runID)
	reviewPath := filepath.Join(dir, ".workflow", "runs", runID, "development", "review.out.md")
	if err := os.MkdirAll(filepath.Dir(reviewPath), 0755); err != nil {
		t.Fatalf("MkdirAll(review dir) error = %v", err)
	}
	if err := os.WriteFile(reviewPath, []byte("# Review\n"), 0644); err != nil {
		t.Fatalf("WriteFile(review.out.md) error = %v", err)
	}

	check, err := checkCompletionAt(dir, runID)
	if err != nil {
		t.Fatalf("checkCompletionAt() error = %v", err)
	}
	if !check.Passed {
		t.Fatalf("checkCompletionAt() Passed = false, missing = %v", check.Missing)
	}
	assertEvidenceType(t, check, "review_report", reviewPath)
}

func TestCompletionCheckRecognizesPhase3ReviewReportEvidence(t *testing.T) {
	dir := t.TempDir()
	runID := "run-phase3-review-report"
	writeCompletionCheckBaseFiles(t, dir, runID)
	paths := []string{
		filepath.Join(dir, ".workflow", "runs", runID, "review", "subtasks", "api", "review-api.out.md"),
		filepath.Join(dir, ".workflow", "runs", runID, "review", "aggregate", "review-aggregate.out.md"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte("# Review\n"), 0644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}

	check, err := checkCompletionAt(dir, runID)
	if err != nil {
		t.Fatalf("checkCompletionAt() error = %v", err)
	}
	if !check.Passed {
		t.Fatalf("checkCompletionAt() Passed = false, missing = %v", check.Missing)
	}
	for _, path := range paths {
		assertEvidenceType(t, check, "review_report", path)
	}
}

func TestCompletionCheckRecognizesAttemptLineageEvidence(t *testing.T) {
	dir := t.TempDir()
	runID := "run-attempt-lineage"
	writeCompletionCheckBaseFiles(t, dir, runID)
	lineagePath := filepath.Join(dir, ".workflow", "runs", runID, "attempts.index.jsonl")
	if err := os.WriteFile(lineagePath, []byte(`{"run_id":"run-attempt-lineage"}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile(attempts.index.jsonl) error = %v", err)
	}

	check, err := checkCompletionAt(dir, runID)
	if err != nil {
		t.Fatalf("checkCompletionAt() error = %v", err)
	}
	if !check.Passed {
		t.Fatalf("checkCompletionAt() Passed = false, missing = %v", check.Missing)
	}
	assertEvidenceType(t, check, "attempt_lineage", lineagePath)
}

func writeCompletionCheckBaseFiles(t *testing.T, dir, runID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0755); err != nil {
		t.Fatalf("MkdirAll(docs) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "output.md"), []byte("# output\n"), 0644); err != nil {
		t.Fatalf("WriteFile(output.md) error = %v", err)
	}
	if err := writePhaseStatusAt(dir, runID, &PhaseStatus{
		Phase:      "development",
		GateStatus: "passed",
		NextAction: "finish",
	}); err != nil {
		t.Fatalf("writePhaseStatusAt() error = %v", err)
	}
}

func assertEvidenceType(t *testing.T, check *CompletionCheck, evidenceType, path string) {
	t.Helper()
	for _, item := range check.EvidenceIndex {
		if item.EvidenceType == evidenceType && item.Path == path && item.Verified {
			return
		}
	}
	t.Fatalf("EvidenceIndex = %+v, want verified %s at %s", check.EvidenceIndex, evidenceType, path)
}
