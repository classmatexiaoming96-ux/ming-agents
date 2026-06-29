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
