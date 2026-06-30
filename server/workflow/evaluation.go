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
	"strings"
	"time"

	"github.com/ming-agents/server/adapter"
)

// RunEvaluation 执行结构化验证并返回分类结果
func RunEvaluation(runCtx context.Context, repoRoot, runID string) (*EvaluationResult, error) {
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

	result.Evidence = collectEvidence(repoRoot, runID)

	if !result.Passed {
		result.FailureClass = classifyFailure(result.TestResults)
		result.RetryAdvice = retryAdviceFor(result.FailureClass)
		result.SubtaskResults = subtaskFailuresFromTestResults(result.TestResults, result.Evidence)
	} else {
		result.FailureClass = FailureClassNone
	}

	if err := writeEvaluationResult(repoRoot, runID, result); err != nil {
		return result, err
	}

	return result, nil
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

func subtaskFailuresFromTestResults(results []TestResult, evidence []EvidenceRef) []SubtaskFailure {
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
			SubtaskID:    result.SubtaskID,
			FailureClass: failureClass,
			Reason:       evaluationFailureReason(result),
			EvidenceRefs: append([]EvidenceRef(nil), evidence...),
			RetryAdvice:  retryAdviceFor(failureClass),
			NextAction:   NextAction(NextActionForFailure(failureClass)),
		})
	}
	return failures
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
	if _, err := runGitOutput(repoRoot, "rev-parse", "--is-inside-work-tree"); err != nil {
		return err
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
