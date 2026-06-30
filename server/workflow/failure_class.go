package workflow

import (
	"context"
	"errors"
	"strings"
)

// ClassifyError maps node/runtime errors into rollback failure classes.
func ClassifyError(err error, reason string) FailureClass {
	if err == nil {
		return FailureClassNone
	}
	if errors.Is(err, ErrApprovalRejected) {
		return FailureClassHumanReject
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return FailureClassTransient
	}

	failureText := normalizeFailureText(err.Error(), reason)
	switch {
	case containsFailureText(failureText, "timeout", "timed out", "deadline exceeded"):
		return FailureClassTransient
	case containsFailureText(failureText, "parser", "parse error", "schema", "contract", "invalid json"):
		return FailureClassContractError
	case containsFailureText(failureText, "missing evidence", "missing artifact", "evidence not found", "artifact not found", "no such file"):
		return FailureClassMissingEvidence
	case containsFailureText(failureText, "go: not found", "npm: not found", "command not found", "permission denied", "network"):
		return FailureClassEnvironmentBlock
	case strings.Contains(failureText, "not found"):
		if containsFailureText(failureText, "artifact", "evidence") {
			return FailureClassMissingEvidence
		}
		return FailureClassEnvironmentBlock
	}

	return FailureClassProductDefect
}

// ClassifyCommandResult maps command exits and output into rollback failure classes.
func ClassifyCommandResult(exitCode int, command, stdout, stderr string) FailureClass {
	if exitCode == 0 {
		return FailureClassNone
	}

	failureText := normalizeFailureText(command, stdout, stderr)
	switch {
	case exitCode == -1:
		return FailureClassValidatorIssue
	case exitCode == 2, exitCode == 126, exitCode == 127:
		return FailureClassEnvironmentBlock
	case containsFailureText(failureText, "go: not found", "npm: not found", "command not found", "permission denied", "network"):
		return FailureClassEnvironmentBlock
	case containsFailureText(failureText, "syntax error", "undefined:", "cannot find"):
		return FailureClassProductDefect
	default:
		return FailureClassProductDefect
	}
}

func normalizeFailureText(parts ...string) string {
	return strings.ToLower(strings.Join(parts, "\n"))
}

func containsFailureText(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
