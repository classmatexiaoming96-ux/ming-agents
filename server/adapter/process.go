package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultAgentTimeout = 5 * time.Minute

type processResult struct {
	Adapter  string   `json:"adapter"`
	Command  []string `json:"command"`
	WorkDir  string   `json:"work_dir,omitempty"`
	ExitCode int      `json:"exit_code"`
	TimedOut bool     `json:"timed_out,omitempty"`
}

func promptFromRequest(req AgentRequest) string {
	if req.Prompt != "" {
		return req.Prompt
	}
	if len(req.RawJSON) > 0 {
		return string(req.RawJSON)
	}
	return ""
}

func effectiveTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return defaultAgentTimeout
}

func mergeExecutionContext(workDir, command string, timeout time.Duration, overrides []ExecutionContext, req AgentRequest) ExecutionContext {
	effective := ExecutionContext{
		WorkDir: workDir,
		Command: command,
		Timeout: timeout,
	}
	if req.Execution.WorkDir != "" {
		effective.WorkDir = req.Execution.WorkDir
	}
	if req.Execution.Command != "" {
		effective.Command = req.Execution.Command
	}
	if req.Execution.Timeout > 0 {
		effective.Timeout = req.Execution.Timeout
	}
	for _, override := range overrides {
		if override.WorkDir != "" {
			effective.WorkDir = override.WorkDir
		}
		if override.Command != "" {
			effective.Command = override.Command
		}
		if override.Timeout > 0 {
			effective.Timeout = override.Timeout
		}
	}
	return effective
}

func marshalProcessResult(meta processResult) json.RawMessage {
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	return raw
}

func runAgentCommand(adapterName, workDir string, timeout time.Duration, command string, args []string, stdin string) (*AgentResult, error) {
	if command == "" {
		return processErrorResult(adapterName, workDir, []string{command}, -1, false, "command is required"), fmt.Errorf("%s adapter: command is required", adapterName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), effectiveTimeout(timeout))
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	err := cmd.Run()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	exitCode := exitCodeFor(err, timedOut)
	meta := processResult{
		Adapter:  adapterName,
		Command:  append([]string{command}, args...),
		WorkDir:  workDir,
		ExitCode: exitCode,
		TimedOut: timedOut,
	}

	result := &AgentResult{
		Output:  stdout.String(),
		RawJSON: marshalProcessResult(meta),
		Summary: fmt.Sprintf("%s adapter exited with code %d", adapterName, exitCode),
	}

	if err == nil && !timedOut {
		return result, nil
	}

	result.Error = processErrorMessage(err, stderr.String(), timedOut)
	if timedOut {
		return result, fmt.Errorf("%s adapter timed out after %s", adapterName, effectiveTimeout(timeout))
	}
	return result, fmt.Errorf("%s adapter: %w", adapterName, err)
}

func processErrorResult(adapterName, workDir string, command []string, exitCode int, timedOut bool, message string) *AgentResult {
	return &AgentResult{
		Error: message,
		RawJSON: marshalProcessResult(processResult{
			Adapter:  adapterName,
			Command:  command,
			WorkDir:  workDir,
			ExitCode: exitCode,
			TimedOut: timedOut,
		}),
		Summary: fmt.Sprintf("%s adapter exited with code %d", adapterName, exitCode),
	}
}

func exitCodeFor(err error, timedOut bool) int {
	if timedOut {
		return -1
	}
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func processErrorMessage(err error, stderr string, timedOut bool) string {
	stderr = strings.TrimSpace(stderr)
	if timedOut {
		if stderr == "" {
			return "process timed out"
		}
		return "process timed out: " + stderr
	}
	if stderr != "" {
		return stderr
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
