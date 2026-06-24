package workflow

import (
	"context"
	"fmt"
	"os"
)

func Run(ctx context.Context, repoRoot, userInput string) (runID string, err error) {
	if repoRoot == "" {
		return "", fmt.Errorf("repoRoot is required")
	}
	clarFile, err := RunClarification(ctx, repoRoot, userInput)
	if err != nil {
		return "", err
	}
	clarRunID, err := readRunIDFromMarkdown(clarFile)
	if err != nil {
		return "", err
	}
	if err := WaitForApproval(ctx, WorkflowNodeSession(repoRoot, clarRunID, "node1").ID, "node1"); err != nil {
		return clarRunID, err
	}
	plan, err := RunPlanning(ctx, repoRoot, clarFile)
	if err != nil {
		return clarRunID, err
	}
	if err := WaitForApproval(ctx, WorkflowNodeSession(repoRoot, clarRunID, "node2").ID, "node2"); err != nil {
		return clarRunID, err
	}
	plan.TaskID = clarRunID
	if _, err := RunDevelopment(ctx, repoRoot, plan); err != nil {
		return clarRunID, err
	}
	return clarRunID, nil
}

func readRunIDFromMarkdown(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	runID := extractMetadataLine(string(data), "run_id")
	if runID == "" {
		return "", fmt.Errorf("run_id missing from %s", path)
	}
	return runID, nil
}
