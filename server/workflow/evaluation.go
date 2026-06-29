package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	_, _ = ReadPhaseStatus(repoRoot, runID)
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
	} else {
		result.FailureClass = "none"
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
	stDir := filepath.Join(repoRoot, ".workflow", "runs", runID, subtaskDir)
	cmdPath := filepath.Join(stDir, "test_command.txt")

	cmdStr := "echo 'no test command configured'"
	if data, err := os.ReadFile(cmdPath); err == nil {
		cmdStr = strings.TrimSpace(string(data))
		if cmdStr == "" {
			cmdStr = "echo 'no test command configured'"
		}
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
	_ = os.MkdirAll(runDir, 0755)
	outPath := filepath.Join(runDir, subtaskDir+"_stdout.txt")
	errPath := filepath.Join(runDir, subtaskDir+"_stderr.txt")
	_ = os.WriteFile(outPath, stdout.Bytes(), 0644)
	stderrPath := ""
	if stderr.Len() > 0 {
		_ = os.WriteFile(errPath, stderr.Bytes(), 0644)
		stderrPath = errPath
	}

	return TestResult{
		TestID:     subtaskDir,
		SubtaskID:  subtaskDir,
		Command:    cmdStr,
		ExitCode:   exitCode,
		Passed:     exitCode == 0,
		StdoutPath: outPath,
		StderrPath: stderrPath,
		DurationMs: time.Since(start).Milliseconds(),
	}
}

func classifyFailure(results []TestResult) string {
	hasRealFailure := false
	hasEnvError := false
	hasValidatorError := false

	for _, r := range results {
		failureText := strings.ToLower(r.Command + "\n" + r.StdoutPath + "\n" + r.StderrPath)
		switch {
		case r.ExitCode == -1:
			hasValidatorError = true
		case r.ExitCode == 2, r.ExitCode == 126, r.ExitCode == 127:
			hasEnvError = true
		case strings.Contains(failureText, "go: not found"), strings.Contains(failureText, "npm: not found"), strings.Contains(failureText, "command not found"), strings.Contains(failureText, "permission denied"):
			hasEnvError = true
		case r.ExitCode != 0:
			hasRealFailure = true
		}
	}

	if hasValidatorError {
		return "validator_issue"
	}
	if hasEnvError {
		return "environment_block"
	}
	if hasRealFailure {
		return "product_defect"
	}
	return "inconclusive"
}

func retryAdviceFor(fc string) string {
	switch fc {
	case "environment_block":
		return "修复环境问题后重试：安装依赖/检查网络/确认权限"
	case "validator_issue":
		return "修复验证工具问题后重试：检查测试框架/断言/超时配置"
	case "transient":
		return "建议重试 1-2 次，flaky test 需要单独处理"
	case "product_defect":
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
		Passed:       failureClass == "none",
	}
	for _, ref := range result.Evidence {
		out.Evidence = append(out.Evidence, EvidenceRef{Type: ref.Type, Path: ref.Path})
	}
	return out
}

func classifyVerificationVerdict(verdict, reason string) string {
	normalizedVerdict := strings.ToLower(strings.TrimSpace(verdict))
	normalizedReason := strings.ToLower(strings.TrimSpace(reason))
	switch normalizedVerdict {
	case "pass":
		return "none"
	case "error":
		return "validator_issue"
	case "fail":
		if strings.Contains(normalizedReason, "go: not found") ||
			strings.Contains(normalizedReason, "npm: not found") ||
			strings.Contains(normalizedReason, "command not found") ||
			strings.Contains(normalizedReason, "permission denied") ||
			strings.Contains(normalizedReason, "network") {
			return "environment_block"
		}
		return "product_defect"
	case "":
		return "inconclusive"
	default:
		return "inconclusive"
	}
}

func retryAdviceForVerification(failureClass, reason string) string {
	if failureClass == "none" {
		return ""
	}
	if strings.TrimSpace(reason) != "" {
		return "验证失败: " + strings.TrimSpace(reason)
	}
	return retryAdviceFor(failureClass)
}

func collectEvidence(repoRoot, runID string) []EvidenceRef {
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	var refs []EvidenceRef
	patterns := []struct{ t, p string }{
		{"build_log", "build.log"},
		{"test_log", "test.log"},
		{"coverage", "coverage.out"},
	}
	for _, p := range patterns {
		path := filepath.Join(runDir, p.p)
		if _, err := os.Stat(path); err == nil {
			refs = append(refs, EvidenceRef{Type: p.t, Path: path})
		}
	}
	return refs
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
