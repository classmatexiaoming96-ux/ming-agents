package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type WorkflowState = RunState

func RunDevelopment(ctx context.Context, repoRoot string, plan *Plan) (*WorkflowState, error) {
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	runID := plan.TaskID
	nodeDir := filepath.Join(repoRoot, ".workflow", "runs", runID, "node3")
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return nil, err
	}
	_ = writeWorkflowState(repoRoot, runID, map[string]NodeStatus{
		"node1": NodeCompleted,
		"node2": NodeCompleted,
		"node3": NodeRunning,
	}, nil)

	results := runDevelopmentSubtasks(ctx, repoRoot, nodeDir, plan, nil, 0)
	report, reviewOut, err := RunReview(ctx, repoRoot, nodeDir, plan, results)
	if err != nil {
		return nil, err
	}
	if !report.Passed {
		retryTargets := blockingRetryTargets(report, results)
		if len(retryTargets) > 0 {
			results = append(results, runDevelopmentSubtasks(ctx, repoRoot, nodeDir, plan, retryTargets, 1)...)
			report, reviewOut, err = RunReview(ctx, repoRoot, nodeDir, plan, results)
			if err != nil {
				return nil, err
			}
		}
	}

	stateStatus := NodeCompleted
	if !report.Passed {
		stateStatus = NodeFailed
	}
	finalState := &WorkflowState{
		RunID: runID,
		Nodes: map[string]NodeStatus{
			"node1": NodeCompleted,
			"node2": NodeCompleted,
			"node3": stateStatus,
		},
		Details: map[string]any{
			"review_passed": report.Passed,
			"issue_count":   len(report.Issues),
		},
	}
	outputPath := filepath.Join(repoRoot, "docs", "output.md")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return nil, err
	}
	if err := writeTextAtomic(outputPath, renderOutputMarkdown(runID, plan, results, report, reviewOut)); err != nil {
		return nil, err
	}
	if err := writeJSONAtomic(filepath.Join(repoRoot, ".workflow", "runs", runID, "state.json"), finalState); err != nil {
		return nil, err
	}
	if !report.Passed {
		return finalState, fmt.Errorf("review found blocking issues")
	}
	return finalState, nil
}

func runDevelopmentSubtasks(ctx context.Context, repoRoot, nodeDir string, plan *Plan, only map[string][]ReviewIssue, retry int) []*SubtaskResult {
	var subtasks []Subtask
	for _, st := range plan.Subtasks {
		if only == nil {
			subtasks = append(subtasks, st)
			continue
		}
		if _, ok := only[st.ID]; ok {
			subtasks = append(subtasks, st)
		}
	}
	results := make([]*SubtaskResult, len(subtasks))
	var wg sync.WaitGroup
	for i, st := range subtasks {
		wg.Add(1)
		go func(i int, st Subtask) {
			defer wg.Done()
			results[i] = runDevelopmentSubtask(ctx, repoRoot, nodeDir, plan, st, i+1, retry, only[st.ID])
		}(i, st)
	}
	wg.Wait()
	return results
}

func runDevelopmentSubtask(ctx context.Context, repoRoot, nodeDir string, plan *Plan, st Subtask, index, retry int, issues []ReviewIssue) *SubtaskResult {
	suffix := fmt.Sprintf("dev-%d", index)
	if retry > 0 {
		suffix = fmt.Sprintf("dev-%d-r%d", index, retry)
	}
	sessionID := NewPTYSessionID(plan.TaskID, "node3", "codex", index)
	if retry > 0 {
		sessionID = fmt.Sprintf("%s-r%d", sessionID, retry)
	}
	prompt := renderDevelopmentPrompt(repoRoot, st, sessionID, plan, issues)
	promptFile := filepath.Join(nodeDir, suffix+".prompt.md")
	outFile := filepath.Join(nodeDir, suffix+".out.md")
	exitFile := filepath.Join(nodeDir, suffix+".exit")
	_ = writeTextAtomic(promptFile, prompt)

	workDir := filepath.Join(repoRoot, filepath.Clean(st.RepoPath))
	result := &SubtaskResult{Subtask: st, SessionID: sessionID, OutFile: outFile, ExitFile: exitFile, Status: "completed"}
	runCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "codex", "exec", prompt)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	result.Output = string(output)
	result.Err = err
	if err != nil {
		result.Status = "failed"
		result.ExitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		}
	} else {
		result.ExitCode = 0
	}
	if runCtx.Err() != nil {
		result.Status = "failed"
		result.Err = runCtx.Err()
		result.ExitCode = 1
	}
	_ = os.WriteFile(outFile, output, 0644)
	_ = os.WriteFile(exitFile, []byte(fmt.Sprintf("%d\n", result.ExitCode)), 0644)
	return result
}

func renderDevelopmentPrompt(repoRoot string, st Subtask, sessionID string, plan *Plan, issues []ReviewIssue) string {
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	var b strings.Builder
	b.WriteString("# Role\n")
	b.WriteString("You are a development agent executing one assigned subtask.\n\n")
	b.WriteString("# Repository Scope\n")
	fmt.Fprintf(&b, "Repo root: %s\nAssigned repo_path: %s\ntmux session: %s\n\n", repoRoot, st.RepoPath, sessionID)
	b.WriteString("# Full Task\n")
	b.WriteString(plan.TaskID)
	b.WriteString("\n\n## Plan\n")
	b.Write(planJSON)
	b.WriteString("\n\n## Subtask\n")
	b.WriteString(st.Description)
	b.WriteString("\n\n## Acceptance Criteria\n")
	for _, criterion := range st.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", criterion)
	}
	if len(issues) > 0 {
		b.WriteString("\n## Review Feedback To Fix\n")
		for _, issue := range issues {
			fmt.Fprintf(&b, "- %s: %s\n", issue.Severity, issue.Description)
			for _, fix := range issue.RequiredFixes {
				fmt.Fprintf(&b, "  - %s\n", fix)
			}
		}
	}
	b.WriteString("\n# Constraints\n")
	b.WriteString("- Work only on files required for this subtask.\n")
	b.WriteString("- Do not revert unrelated user or agent changes.\n")
	b.WriteString("- Add or update focused tests when behavior changes.\n")
	b.WriteString("- At completion, summarize changed files, tests run, and any blocked criteria.\n")
	return b.String()
}

func RunReview(ctx context.Context, repoRoot, nodeDir string, plan *Plan, results []*SubtaskResult) (*ReviewReport, string, error) {
	diffFile, _, err := writeReviewInputs(repoRoot, nodeDir, plan, results)
	if err != nil {
		return nil, "", err
	}
	prompt := renderReviewPrompt(plan, results, diffFile)
	promptFile := filepath.Join(nodeDir, "review.prompt.md")
	outFile := filepath.Join(nodeDir, "review.out.md")
	exitFile := filepath.Join(nodeDir, "review.exit")
	_ = writeTextAtomic(promptFile, prompt)
	output, err := runCodexPrompt(ctx, repoRoot, prompt, 30*time.Minute)
	exitCode := 0
	if err != nil {
		exitCode = 1
		output = err.Error()
	}
	_ = os.WriteFile(outFile, []byte(output), 0644)
	_ = os.WriteFile(exitFile, []byte(fmt.Sprintf("%d\n", exitCode)), 0644)
	report := ParseReviewReport(output)
	if err != nil {
		report.Passed = false
		report.Issues = append(report.Issues, ReviewIssue{Severity: "blocking", Description: err.Error()})
	}
	return report, output, nil
}

func blockingRetryTargets(report *ReviewReport, results []*SubtaskResult) map[string][]ReviewIssue {
	targets := map[string][]ReviewIssue{}
	if report == nil {
		return targets
	}
	bySession := map[string]string{}
	for _, result := range results {
		if result != nil {
			bySession[result.SessionID] = result.Subtask.ID
		}
	}
	fallback := ""
	if len(results) > 0 && results[0] != nil {
		fallback = results[0].Subtask.ID
	}
	for _, issue := range report.Issues {
		if normalizeSeverity(issue.Severity) != "blocking" {
			continue
		}
		target := issue.SubtaskID
		if target == "" && issue.SessionID != "" {
			target = bySession[issue.SessionID]
		}
		if target == "" {
			target = fallback
		}
		if target != "" {
			targets[target] = append(targets[target], issue)
		}
	}
	return targets
}

func renderOutputMarkdown(runID string, plan *Plan, results []*SubtaskResult, report *ReviewReport, reviewOut string) string {
	var b strings.Builder
	b.WriteString("# Workflow Output\n\n")
	fmt.Fprintf(&b, "run_id: %s\nnode: 3\nstate: %s\n\n", runID, outputState(report))
	b.WriteString("## Development Results\n\n")
	for _, result := range results {
		if result == nil {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", result.Subtask.ID)
		fmt.Fprintf(&b, "session: %s\nstatus: %s\nexit_code: %d\nout_file: %s\n\n", result.SessionID, result.Status, result.ExitCode, filepath.ToSlash(result.OutFile))
	}
	b.WriteString("## Review Summary\n\n")
	if report != nil && report.Summary != "" {
		b.WriteString(report.Summary)
		b.WriteString("\n\n")
	}
	if report != nil {
		fmt.Fprintf(&b, "passed: %t\nissues: %d\n\n", report.Passed, len(report.Issues))
	}
	if strings.TrimSpace(reviewOut) != "" {
		b.WriteString("## Review Output\n\n")
		b.WriteString(strings.TrimSpace(reviewOut))
		b.WriteString("\n")
	}
	_ = plan
	return b.String()
}

func outputState(report *ReviewReport) NodeStatus {
	if report != nil && report.Passed {
		return NodeCompleted
	}
	return NodeFailed
}
