package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func CheckCompletion(runID string) (*CompletionCheck, error) {
	return checkCompletionAt(".", runID)
}

func checkCompletionAt(baseDir, runID string) (*CompletionCheck, error) {
	check := &CompletionCheck{
		RunID:     runID,
		CheckedAt: time.Now(),
		Passed:    true,
	}

	statusPath := filepath.Join(baseDir, ".workflow", "runs", runID, "phase_status.json")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		check.Passed = false
		check.Missing = append(check.Missing, "phase_status.json not found")
		return check, nil
	}

	var status PhaseStatus
	if err := json.Unmarshal(data, &status); err != nil {
		check.Passed = false
		check.Missing = append(check.Missing, "phase_status.json parse error: "+err.Error())
		return check, nil
	}

	outputPath := filepath.Join(baseDir, "docs", "output.md")
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		runOutputPath := filepath.Join(baseDir, ".workflow", "runs", runID, "docs", "output.md")
		if _, runErr := os.Stat(runOutputPath); os.IsNotExist(runErr) {
			check.Passed = false
			check.Missing = append(check.Missing, "docs/output.md not found")
			check.BlockedItems = append(check.BlockedItems, BlockedItem{
				SubtaskID: "output",
				Reason:    "output.md does not exist",
			})
		} else if runErr == nil {
			outputPath = runOutputPath
		}
	}
	if _, err := os.Stat(outputPath); err == nil {
		check.EvidenceIndex = append(check.EvidenceIndex, EvidenceItem{
			SubtaskID:    "output",
			EvidenceType: "document",
			Path:         outputPath,
			Verified:     true,
		})
	}

	runDir := filepath.Join(baseDir, ".workflow", "runs", runID)
	entries, _ := os.ReadDir(runDir)
	for _, entry := range entries {
		if entry.IsDir() && (entry.Name() == "code" || entry.Name() == "src" || entry.Name() == "changes") {
			check.EvidenceIndex = append(check.EvidenceIndex, EvidenceItem{
				SubtaskID:    entry.Name(),
				EvidenceType: "code_artifacts",
				Path:         filepath.Join(runDir, entry.Name()),
				Verified:     true,
			})
		}
	}
	addCompletionEvidenceIfExists(check, "coverage", "coverage", filepath.Join(runDir, "coverage.out"))
	addCompletionEvidenceIfExists(check, "attempt_lineage", "attempt_lineage", filepath.Join(runDir, "attempts.index.jsonl"))
	addReviewReportEvidence(check, runDir)

	if len(check.Missing) > 0 {
		check.Passed = false
	}

	return check, nil
}

func addCompletionEvidenceIfExists(check *CompletionCheck, subtaskID, evidenceType, path string) {
	if _, err := os.Stat(path); err == nil {
		check.EvidenceIndex = append(check.EvidenceIndex, EvidenceItem{
			SubtaskID:    subtaskID,
			EvidenceType: evidenceType,
			Path:         path,
			Verified:     true,
		})
	}
}

func addReviewReportEvidence(check *CompletionCheck, runDir string) {
	_ = filepath.WalkDir(runDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "review.out.md" {
			return nil
		}
		check.EvidenceIndex = append(check.EvidenceIndex, EvidenceItem{
			SubtaskID:    "review",
			EvidenceType: "review_report",
			Path:         path,
			Verified:     true,
		})
		return nil
	})
}
