package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// WritePhaseStatus 写入 phase_status.json
func WritePhaseStatus(runID string, status *PhaseStatus) error {
	return writePhaseStatusAt(".", runID, status)
}

func writePhaseStatusAt(baseDir, runID string, status *PhaseStatus) error {
	status.RunID = runID
	status.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	runDir := filepath.Join(baseDir, ".workflow", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "phase_status.json"), data, 0644)
}
