package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func writeJSONAtomic(path string, v any) error {
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	return os.Rename(tmp, path)
}

func writeTextAtomic(path string, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	return os.Rename(tmp, path)
}

func NewPTYSessionID(runID, node, role string, index int) string {
	safeRole := strings.NewReplacer("/", "-", "_", "-").Replace(role)
	return fmt.Sprintf("pty-%s-%s-%s-%d", runID, node, safeRole, index)
}

func WaitSessionExit(ctx context.Context, exitFile, outFile string, timeout time.Duration) (exitCode int, output string, err error) {
	deadline := time.After(timeout)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return -1, "", ctx.Err()
		case <-deadline:
			return -1, "", fmt.Errorf("timeout waiting for %s", exitFile)
		case <-tick.C:
			if _, statErr := os.Stat(exitFile); statErr == nil {
				exitBytes, _ := os.ReadFile(exitFile)
				code := strings.TrimSpace(string(exitBytes))
				fmt.Sscanf(code, "%d", &exitCode)
				outBytes, _ := os.ReadFile(outFile)
				return exitCode, string(outBytes), nil
			}
		}
	}
}
