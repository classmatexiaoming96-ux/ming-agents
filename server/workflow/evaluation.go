package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ming-agents/server/adapter"
)

// RunEvaluation 执行结构化验证并返回分类结果
func RunEvaluation(runCtx context.Context, repoRoot, runID string) (*EvaluationResult, error) {
	return RunEvaluationWithPlan(runCtx, repoRoot, runID, nil)
}

// RunEvaluationWithPlan executes validation with optional planning context for failure attribution.
func RunEvaluationWithPlan(runCtx context.Context, repoRoot, runID string, plan *Plan) (*EvaluationResult, error) {
	result := &EvaluationResult{
		RunID:       runID,
		EvaluatedAt: time.Now(),
		Passed:      true,
	}

	_, err := ReadPhaseStatus(repoRoot, runID)
	if err != nil {
		log.Printf("RunEvaluation: ReadPhaseStatus: %v", err)
	}
	subtasks := discoverSubtasks(repoRoot, runID)
	for _, st := range subtasks {
		tr := runTestForSubtask(runCtx, repoRoot, runID, st)
		result.TestResults = append(result.TestResults, tr)
		if !tr.Passed {
			result.Passed = false
		}
	}
	if len(result.TestResults) == 0 {
		result.Passed = true
	}

	changedFiles, changedErr := changedFilesForAttribution(repoRoot)
	applyCoverageGate(runCtx, repoRoot, runID, plan, changedFiles, changedErr, result)
	result.Evidence = collectEvidence(repoRoot, runID)

	if !result.Passed {
		result.FailureClass = classifyFailure(result.TestResults)
		result.RetryAdvice = retryAdviceFor(result.FailureClass)
		result.SubtaskResults = subtaskFailuresFromTestResults(result.TestResults, result.Evidence, plan, changedFiles)
	} else {
		result.FailureClass = FailureClassNone
	}

	if err := writeEvaluationResult(repoRoot, runID, result); err != nil {
		return result, err
	}

	return result, nil
}

func changedFilesForAttribution(repoRoot string) ([]string, error) {
	changedFiles, err := ChangedFiles(repoRoot)
	if err != nil {
		return nil, err
	}
	return changedFiles, nil
}

func applyCoverageGate(runCtx context.Context, repoRoot, runID string, plan *Plan, changedFiles []string, changedErr error, result *EvaluationResult) {
	if changedErr != nil {
		// A failure to inspect git state is an environment problem, not "no Go changes".
		// Surface it as a blocking environment_block instead of silently skipping the gate.
		tr := TestResult{
			TestID:       "coverage",
			Command:      "git diff --name-only (changed file detection)",
			Passed:       false,
			ExitCode:     1,
			FailureClass: failureClassFromError(changedErr, FailureClassEnvironmentBlock),
		}
		tr.SubtaskID = AttributeFailureToSubtask(plan, nil, tr)
		result.Passed = false
		result.TestResults = append(result.TestResults, tr)
		return
	}
	if !changedFilesHaveGoCode(changedFiles) {
		return
	}

	tr := TestResult{
		TestID:  "coverage",
		Command: "go test -cover -coverprofile=.workflow/runs/" + runID + "/coverage.out ./...",
		Passed:  true,
	}
	coverage, err := RunCoverageCommand(runCtx, repoRoot, runID)
	if err != nil {
		tr.Passed = false
		tr.ExitCode = 1
		tr.FailureClass = failureClassFromError(err, FailureClassValidatorIssue)
		tr.SubtaskID = AttributeFailureToSubtask(plan, changedFiles, tr)
		result.Passed = false
		result.TestResults = append(result.TestResults, tr)
		return
	}
	if coverage.TotalPercent < 100.0 {
		tr.Passed = false
		tr.ExitCode = 1
		tr.FailureClass = FailureClassProductDefect
		tr.SubtaskID = AttributeFailureToSubtask(plan, changedFiles, tr)
		result.Passed = false
	}
	result.TestResults = append(result.TestResults, tr)
}

func failureClassFromError(err error, fallback FailureClass) FailureClass {
	var classified interface {
		FailureClass() FailureClass
	}
	if errors.As(err, &classified) {
		if failureClass := classified.FailureClass(); failureClass != "" {
			return failureClass
		}
	}
	return fallback
}

// ReadPhaseStatus 读取 phase_status.json（如果不存在返回 nil）
func ReadPhaseStatus(repoRoot, runID string) (*PhaseStatus, error) {
	path := filepath.Join(repoRoot, ".workflow", "runs", runID, "phase_status.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s PhaseStatus
	err = json.Unmarshal(data, &s)
	return &s, err
}

func discoverSubtasks(repoRoot, runID string) []string {
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	entries, _ := os.ReadDir(runDir)
	var subtasks []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "subtask-") {
			subtasks = append(subtasks, e.Name())
		}
	}
	sort.Strings(subtasks)
	return subtasks
}

func runTestForSubtask(runCtx context.Context, repoRoot, runID, subtaskDir string) TestResult {
	unit := RollbackUnit{Scope: "command:" + subtaskDir, MaxAttempts: 2, ReusePolicy: SessionReuseNewSession}
	attempt := 0
	for {
		result := executeTestCommand(runCtx, repoRoot, runID, subtaskDir)
		writeEvaluationAttempt(repoRoot, runID, unit.Scope, attempt, result)
		if result.Passed || !evaluationFailureRetryable(result.FailureClass) || attempt+1 >= unit.MaxAttempts {
			return result
		}
		attempt++
	}
}

func executeTestCommand(runCtx context.Context, repoRoot, runID, subtaskDir string) TestResult {
	stDir := filepath.Join(repoRoot, ".workflow", "runs", runID, subtaskDir)
	cmdPath := filepath.Join(stDir, "test_command.txt")

	cmdStr := "echo 'no test command configured'"
	if data, err := os.ReadFile(cmdPath); err == nil {
		cmdStr = strings.TrimSpace(string(data))
		if cmdStr == "" {
			cmdStr = "echo 'no test command configured'"
		}
	} else {
		// 文件不存在时写入默认值
		cmdStr = "echo 'no test command configured'"
		_ = os.WriteFile(cmdPath, []byte(cmdStr+"\n"), 0644)
		log.Printf("runTestForSubtask: cannot read %s: %v, using fallback", cmdPath, err)
	}

	start := time.Now()
	cmd := exec.CommandContext(runCtx, "sh", "-c", cmdStr)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		log.Printf("runTestForSubtask: mkdir %s: %v", runDir, err)
	}
	outPath := filepath.Join(runDir, subtaskDir+"_stdout.txt")
	errPath := filepath.Join(runDir, subtaskDir+"_stderr.txt")
	if err := os.WriteFile(outPath, stdout.Bytes(), 0644); err != nil {
		log.Printf("runTestForSubtask: write %s: %v", outPath, err)
	}
	stderrPath := ""
	if stderr.Len() > 0 {
		if err := os.WriteFile(errPath, stderr.Bytes(), 0644); err != nil {
			log.Printf("runTestForSubtask: write %s: %v", errPath, err)
		}
		stderrPath = errPath
	}

	failureClass := FailureClassNone
	if exitCode != 0 {
		failureClass = classifyCommandResult(exitCode, cmdStr, stdout.String(), stderr.String())
	}

	return TestResult{
		TestID:       subtaskDir,
		SubtaskID:    subtaskDir,
		Command:      cmdStr,
		ExitCode:     exitCode,
		Passed:       exitCode == 0,
		StdoutPath:   outPath,
		StderrPath:   stderrPath,
		DurationMs:   time.Since(start).Milliseconds(),
		FailureClass: failureClass,
	}
}

func evaluationFailureRetryable(fc FailureClass) bool {
	switch fc {
	case FailureClassTransient, FailureClassValidatorIssue, FailureClassMissingEvidence, FailureClassInconclusive:
		return true
	default:
		return false
	}
}

func writeEvaluationAttempt(repoRoot, runID, scope string, attempt int, result TestResult) {
	outcome := &AttemptOutcome{
		Status:       "completed",
		Passed:       result.Passed,
		FailureClass: result.FailureClass,
		ArtifactRefs: []ArtifactRef{
			{Type: ArtifactTypeOutput, Path: result.StdoutPath, Description: "stdout"},
		},
	}
	if !result.Passed {
		outcome.Status = "failed"
		outcome.Reason = evaluationFailureReason(result)
	}
	if result.StderrPath != "" {
		outcome.ArtifactRefs = append(outcome.ArtifactRefs, ArtifactRef{Type: ArtifactTypeLog, Path: result.StderrPath, Description: "stderr"})
	}
	_ = writeAttemptEvent(repoRoot, runID, "evaluation", NodeKindEvaluation, scope, "command", "", attempt, attempt-1, "command", result.FailureClass, string(result.FailureClass), "", outcome, nil, "", result.StdoutPath, result.StderrPath)
}

func classifyFailure(results []TestResult) FailureClass {
	highest := FailureClassNone
	for _, r := range results {
		failureClass := r.FailureClass
		if failureClass == "" {
			continue
		}
		if failureClassPriority(failureClass) > failureClassPriority(highest) {
			highest = failureClass
		}
	}
	return highest
}

func failureClassPriority(failureClass FailureClass) int {
	switch failureClass {
	case FailureClassValidatorIssue:
		return 4
	case FailureClassEnvironmentBlock:
		return 3
	case FailureClassProductDefect:
		return 2
	case FailureClassInconclusive:
		return 1
	case FailureClassNone, "":
		return 0
	default:
		return 0
	}
}

func retryAdviceFor(fc FailureClass) string {
	switch fc {
	case FailureClassEnvironmentBlock:
		return "修复环境问题后重试：安装依赖/检查网络/确认权限"
	case FailureClassValidatorIssue:
		return "修复验证工具问题后重试：检查测试框架/断言/超时配置"
	case FailureClassTransient:
		return "建议重试 1-2 次，flaky test 需要单独处理"
	case FailureClassProductDefect:
		return "需要修复代码问题后重试"
	default:
		return "检查验证结果后重试"
	}
}

func evaluationResultFromVerification(result *adapter.VerificationResult) *EvaluationResult {
	if result == nil {
		return nil
	}
	failureClass := classifyVerificationVerdict(result.Verdict, result.Reason)
	out := &EvaluationResult{
		RunID:        result.RunID,
		EvaluatedAt:  result.EvaluatedAt,
		FailureClass: failureClass,
		RetryAdvice:  retryAdviceForVerification(failureClass, result.Reason),
		Passed:       failureClass == FailureClassNone,
	}
	for _, ref := range result.Evidence {
		out.Evidence = append(out.Evidence, EvidenceRef{Type: EvidenceType(ref.Type), Path: ref.Path})
	}
	return out
}

func classifyVerificationVerdict(verdict, reason string) FailureClass {
	normalizedVerdict := strings.ToLower(strings.TrimSpace(verdict))
	normalizedReason := strings.ToLower(strings.TrimSpace(reason))
	switch normalizedVerdict {
	case "pass":
		return FailureClassNone
	case "error":
		return FailureClassValidatorIssue
	case "fail":
		if strings.Contains(normalizedReason, "go: not found") ||
			strings.Contains(normalizedReason, "npm: not found") ||
			strings.Contains(normalizedReason, "command not found") ||
			strings.Contains(normalizedReason, "permission denied") ||
			strings.Contains(normalizedReason, "network") {
			return FailureClassEnvironmentBlock
		}
		return FailureClassProductDefect
	case "":
		return FailureClassInconclusive
	default:
		return FailureClassInconclusive
	}
}

func retryAdviceForVerification(failureClass FailureClass, reason string) string {
	if failureClass == FailureClassNone {
		return ""
	}
	if strings.TrimSpace(reason) != "" {
		return "验证失败: " + strings.TrimSpace(reason)
	}
	return retryAdviceFor(failureClass)
}

func classifyCommandResult(exitCode int, command, stdout, stderr string) FailureClass {
	return ClassifyCommandResult(exitCode, command, stdout, stderr)
}

func subtaskFailuresFromTestResults(results []TestResult, evidence []EvidenceRef, plan *Plan, changedFiles []string) []SubtaskFailure {
	var failures []SubtaskFailure
	for _, result := range results {
		if result.Passed {
			continue
		}
		failureClass := result.FailureClass
		if failureClass == "" {
			failureClass = classifyCommandResult(result.ExitCode, result.Command, "", "")
		}
		failures = append(failures, SubtaskFailure{
			SubtaskID:    AttributeFailureToSubtask(plan, changedFiles, result),
			FailureClass: failureClass,
			Reason:       evaluationFailureReason(result),
			EvidenceRefs: append([]EvidenceRef(nil), evidence...),
			RetryAdvice:  retryAdviceFor(failureClass),
			NextAction:   NextAction(NextActionForFailure(failureClass)),
		})
	}
	return failures
}

// AttributeFailureToSubtask returns the single subtask responsible for a failure.
// Ambiguous plan matches intentionally return "" so callers treat the failure as run-level.
func AttributeFailureToSubtask(plan *Plan, changedFiles []string, test TestResult) string {
	if plan == nil {
		return test.SubtaskID
	}
	changed := normalizedPathSet(changedFiles)
	if len(changed) > 0 {
		if subtaskID, ok, ambiguous := singlePlannedFileMatch(plan, changed); ok || ambiguous {
			if ambiguous {
				return ""
			}
			return subtaskID
		}
		if subtaskID, ok, ambiguous := singleRepoPathMatch(plan, changed); ok || ambiguous {
			if ambiguous {
				return ""
			}
			return subtaskID
		}
	}
	return test.SubtaskID
}

func singlePlannedFileMatch(plan *Plan, changed map[string]struct{}) (string, bool, bool) {
	matches := map[string]struct{}{}
	for _, subtask := range plan.Subtasks {
		for _, plannedFile := range subtask.PlannedFiles {
			path := normalizeGitRelativePath(plannedFile)
			if path == "" {
				continue
			}
			if _, ok := changed[path]; ok {
				matches[subtask.ID] = struct{}{}
			}
		}
	}
	return singleMatch(matches)
}

func singleRepoPathMatch(plan *Plan, changed map[string]struct{}) (string, bool, bool) {
	matches := map[string]struct{}{}
	for _, subtask := range plan.Subtasks {
		repoPath := normalizeGitRelativePath(subtask.RepoPath)
		if repoPath == "" {
			continue
		}
		for changedFile := range changed {
			if changedFile == repoPath || strings.HasPrefix(changedFile, repoPath+"/") {
				matches[subtask.ID] = struct{}{}
			}
		}
	}
	return singleMatch(matches)
}

func normalizedPathSet(paths []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, path := range paths {
		normalized := normalizeGitRelativePath(path)
		if normalized == "" {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

func singleMatch(matches map[string]struct{}) (string, bool, bool) {
	if len(matches) == 0 {
		return "", false, false
	}
	if len(matches) > 1 {
		return "", false, true
	}
	for subtaskID := range matches {
		return subtaskID, true, false
	}
	return "", false, false
}

func evaluationFailureReason(result TestResult) string {
	if result.StderrPath != "" {
		return "test command failed; see " + result.StderrPath
	}
	if result.StdoutPath != "" {
		return "test command failed; see " + result.StdoutPath
	}
	return "test command failed"
}

func collectEvidence(repoRoot, runID string) []EvidenceRef {
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	var refs []EvidenceRef
	patterns := []struct {
		t EvidenceType
		p string
	}{
		{EvidenceTypeBuildLog, "build.log"},
		{EvidenceTypeTestLog, "test.log"},
		{EvidenceTypeCoverage, "coverage.out"},
	}
	for _, p := range patterns {
		path := filepath.Join(runDir, p.p)
		if _, err := os.Stat(path); err == nil {
			refs = append(refs, EvidenceRef{Type: p.t, Path: path})
		}
	}
	return refs
}

type CoverageCommandResult struct {
	RunID        string
	CoveragePath string
	TotalPercent float64
	TestOutput   string
	ToolOutput   string
}

// RunCoverageCommand runs repository-wide Go coverage and parses the total percentage.
// The go commands run in the nearest Go module directory at or below repoRoot, so
// repositories whose go.mod lives in a subdirectory (not the git top-level) are supported.
func RunCoverageCommand(ctx context.Context, repoRoot, runID string) (*CoverageCommandResult, error) {
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, &classifiedEvaluationError{
			op:           "prepare coverage output directory",
			err:          err,
			failureClass: FailureClassEnvironmentBlock,
		}
	}

	moduleDir := goModuleDir(repoRoot)
	coveragePath := filepath.Join(runDir, "coverage.out")
	testArgs := []string{"test", "-cover", "-coverprofile=" + coveragePath, "./..."}
	testOutput, err := runGoCommandOutput(ctx, moduleDir, testArgs...)
	if err != nil {
		return nil, err
	}

	toolArgs := []string{"tool", "cover", "-func=" + coveragePath}
	toolOutput, err := runGoCommandOutput(ctx, moduleDir, toolArgs...)
	if err != nil {
		return nil, err
	}
	total, err := parseGoCoverTotalPercent(string(toolOutput))
	if err != nil {
		return nil, &classifiedEvaluationError{
			op:           "parse go tool cover total",
			err:          err,
			output:       string(toolOutput),
			failureClass: FailureClassValidatorIssue,
		}
	}

	return &CoverageCommandResult{
		RunID:        runID,
		CoveragePath: coveragePath,
		TotalPercent: total,
		TestOutput:   string(testOutput),
		ToolOutput:   string(toolOutput),
	}, nil
}

func runGoCommandOutput(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, &classifiedEvaluationError{
			op:           fmt.Sprintf("go %s", strings.Join(args, " ")),
			err:          err,
			output:       string(out),
			failureClass: classifyCoverageCommandFailure(exitCodeFromError(err), strings.Join(append([]string{"go"}, args...), " "), string(out)),
		}
	}
	return out, nil
}

// goModuleDir returns the directory to run go commands in for coverage. It prefers
// repoRoot when it directly contains a go.mod, otherwise it returns the shallowest
// go.mod directory found below repoRoot. When no module is found it falls back to
// repoRoot so the existing failure classification still applies.
func goModuleDir(repoRoot string) string {
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err == nil {
		return repoRoot
	}
	best := ""
	bestDepth := -1
	_ = filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != repoRoot && (name == ".git" || name == ".workflow" || name == "node_modules" || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		dir := filepath.Dir(path)
		depth := strings.Count(filepath.ToSlash(strings.TrimPrefix(dir, repoRoot)), "/")
		if bestDepth == -1 || depth < bestDepth {
			best = dir
			bestDepth = depth
		}
		return nil
	})
	if best != "" {
		return best
	}
	return repoRoot
}

func classifyCoverageCommandFailure(exitCode int, command, output string) FailureClass {
	failureClass := ClassifyCommandResult(exitCode, command, output, output)
	if failureClass == FailureClassEnvironmentBlock {
		return FailureClassEnvironmentBlock
	}
	return FailureClassValidatorIssue
}

func exitCodeFromError(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func parseGoCoverTotalPercent(output string) (float64, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "total:" {
			continue
		}
		value := strings.TrimSuffix(fields[len(fields)-1], "%")
		percent, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, err
		}
		return percent, nil
	}
	return 0, errors.New("missing total coverage line")
}

type classifiedEvaluationError struct {
	op           string
	err          error
	output       string
	failureClass FailureClass
}

func (e *classifiedEvaluationError) Error() string {
	if e == nil {
		return ""
	}
	msg := e.op
	if e.err != nil {
		msg += ": " + e.err.Error()
	}
	if strings.TrimSpace(e.output) != "" {
		msg += ": " + strings.TrimSpace(e.output)
	}
	return msg
}

func (e *classifiedEvaluationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *classifiedEvaluationError) FailureClass() FailureClass {
	if e == nil {
		return FailureClassNone
	}
	return e.failureClass
}

// ChangedFiles returns staged and unstaged repo-relative paths changed from git state.
func ChangedFiles(repoRoot string) ([]string, error) {
	if err := ensureGitRepo(repoRoot); err != nil {
		return nil, err
	}

	changed := map[string]struct{}{}
	for _, args := range [][]string{
		{"diff", "--name-only"},
		{"diff", "--cached", "--name-only"},
	} {
		out, err := runGitOutput(repoRoot, args...)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(out), "\n") {
			path := normalizeGitRelativePath(line)
			if path == "" {
				continue
			}
			changed[path] = struct{}{}
		}
	}

	paths := make([]string, 0, len(changed))
	for path := range changed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func ensureGitRepo(repoRoot string) error {
	out, err := runGitOutput(repoRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return &classifiedEvaluationError{
			op:           "ensure git repo",
			err:          err,
			failureClass: FailureClassEnvironmentBlock,
		}
	}
	topLevel, err := filepath.Abs(strings.TrimSpace(string(out)))
	if err != nil {
		return &classifiedEvaluationError{
			op:           "ensure git repo top-level",
			err:          err,
			failureClass: FailureClassEnvironmentBlock,
		}
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return &classifiedEvaluationError{
			op:           "ensure git repo root",
			err:          err,
			failureClass: FailureClassEnvironmentBlock,
		}
	}
	if filepath.Clean(topLevel) != filepath.Clean(root) {
		return &classifiedEvaluationError{
			op:           "ensure git repo top-level",
			err:          fmt.Errorf("repoRoot %q does not match git top-level %q", filepath.Clean(root), filepath.Clean(topLevel)),
			failureClass: FailureClassEnvironmentBlock,
		}
	}
	return nil
}

func runGitOutput(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, &classifiedEvaluationError{
			op:           fmt.Sprintf("git %s", strings.Join(args, " ")),
			err:          err,
			output:       string(out),
			failureClass: FailureClassEnvironmentBlock,
		}
	}
	return out, nil
}

func normalizeGitRelativePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.TrimPrefix(path, "./")
	if path == "." {
		return ""
	}
	return path
}

func changedFilesHaveGoCode(paths []string) bool {
	for _, path := range paths {
		if strings.HasSuffix(normalizeGitRelativePath(path), ".go") {
			return true
		}
	}
	return false
}

func writeEvaluationResult(repoRoot, runID string, result *EvaluationResult) error {
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(runDir, "evaluation.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
