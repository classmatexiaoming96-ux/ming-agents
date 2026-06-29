package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CheckReuseAck checks whether the current working directory has an accepted
// reuse-ack record for the run phase.
func CheckReuseAck(ctx context.Context, runID, phase string) (bool, error) {
	return CheckReuseAckAt(ctx, ".", runID, phase)
}

// CheckReuseAckAt checks whether repoRoot has an accepted reuse-ack record for
// the run phase. Missing or unaccepted records are blocking, not exceptional.
func CheckReuseAckAt(ctx context.Context, repoRoot, runID, phase string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	ackFile := filepath.Join(repoRoot, ".workflow", "runs", runID, "reuse-ack.json")
	data, err := os.ReadFile(ackFile)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var ack ReuseAck
	if err := json.Unmarshal(data, &ack); err != nil {
		return false, err
	}
	if ack.RunID != "" && ack.RunID != runID {
		return false, nil
	}
	if ack.Phase != "" && ack.Phase != phase {
		return false, nil
	}
	return ack.Accepted, nil
}

// WriteReuseAckAt persists a ReuseAck JSON for a run phase.
// It creates the .workflow/runs/<runID>/ directory if needed,
// then writes reuse-ack.json atomically.
func WriteReuseAckAt(ctx context.Context, repoRoot, runID, phase string, ack ReuseAck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir .workflow/runs/%s: %w", runID, err)
	}
	ack.RunID = runID
	ack.Phase = phase
	return writeJSONAtomic(filepath.Join(dir, "reuse-ack.json"), ack)
}
