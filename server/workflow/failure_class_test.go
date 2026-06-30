package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		reason string
		want   FailureClass
	}{
		{name: "nil error", want: FailureClassNone},
		{name: "approval rejected", err: fmt.Errorf("gate failed: %w", ErrApprovalRejected), want: FailureClassHumanReject},
		{name: "context canceled", err: context.Canceled, want: FailureClassTransient},
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: FailureClassTransient},
		{name: "timeout text", err: errors.New("validator timed out waiting for prompt"), want: FailureClassTransient},
		{name: "go not found", err: errors.New("go: not found"), want: FailureClassEnvironmentBlock},
		{name: "npm not found", err: errors.New("npm: not found"), want: FailureClassEnvironmentBlock},
		{name: "command not found", err: errors.New("sh: command not found: pnpm"), want: FailureClassEnvironmentBlock},
		{name: "permission denied", err: errors.New("permission denied opening workspace"), want: FailureClassEnvironmentBlock},
		{name: "network", err: errors.New("network connection reset"), want: FailureClassEnvironmentBlock},
		{name: "parser", err: errors.New("parser failed on review output"), want: FailureClassContractError},
		{name: "schema", err: errors.New("schema validation failed"), want: FailureClassContractError},
		{name: "contract", err: errors.New("contract mismatch in verifier response"), want: FailureClassContractError},
		{name: "invalid json", err: errors.New("invalid json in adapter output"), want: FailureClassContractError},
		{name: "missing evidence", err: errors.New("missing evidence for failed test"), want: FailureClassMissingEvidence},
		{name: "missing artifact", err: errors.New("missing artifact evaluation.json"), want: FailureClassMissingEvidence},
		{name: "no such file", err: errors.New("open evidence.log: no such file or directory"), want: FailureClassMissingEvidence},
		{name: "not found with evidence reason", err: errors.New("not found"), reason: "evidence lookup failed", want: FailureClassMissingEvidence},
		{name: "not found without evidence reason", err: errors.New("not found"), want: FailureClassEnvironmentBlock},
		{name: "default", err: errors.New("unit test assertion failed"), want: FailureClassProductDefect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyError(tt.err, tt.reason); got != tt.want {
				t.Fatalf("ClassifyError(%v, %q) = %q, want %q", tt.err, tt.reason, got, tt.want)
			}
		})
	}
}

func TestClassifyCommandResult(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		command  string
		stdout   string
		stderr   string
		want     FailureClass
	}{
		{name: "exit zero", exitCode: 0, want: FailureClassNone},
		{name: "validator issue", exitCode: -1, want: FailureClassValidatorIssue},
		{name: "exit two environment", exitCode: 2, want: FailureClassEnvironmentBlock},
		{name: "exit 126 environment", exitCode: 126, want: FailureClassEnvironmentBlock},
		{name: "exit 127 environment", exitCode: 127, want: FailureClassEnvironmentBlock},
		{name: "environment text", exitCode: 1, stderr: "network unavailable", want: FailureClassEnvironmentBlock},
		{name: "syntax error", exitCode: 1, stderr: "syntax error near unexpected token", want: FailureClassProductDefect},
		{name: "undefined", exitCode: 1, stderr: "undefined: handler", want: FailureClassProductDefect},
		{name: "cannot find", exitCode: 1, stderr: "cannot find package api", want: FailureClassProductDefect},
		{name: "default nonzero", exitCode: 1, stderr: "expected 200 got 500", want: FailureClassProductDefect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyCommandResult(tt.exitCode, tt.command, tt.stdout, tt.stderr)
			if got != tt.want {
				t.Fatalf("ClassifyCommandResult(%d, %q, %q, %q) = %q, want %q", tt.exitCode, tt.command, tt.stdout, tt.stderr, got, tt.want)
			}
		})
	}
}
