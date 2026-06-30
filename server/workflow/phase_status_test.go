package workflow

import "testing"

func TestNextActionForFailure(t *testing.T) {
	tests := []struct {
		name string
		fc   FailureClass
		want string
	}{
		{name: "product defect", fc: FailureClassProductDefect, want: "retry_generator"},
		{name: "environment block", fc: FailureClassEnvironmentBlock, want: "fix_environment"},
		{name: "validator issue", fc: FailureClassValidatorIssue, want: "fix_environment"},
		{name: "contract error", fc: FailureClassContractError, want: "retry_report"},
		{name: "missing evidence", fc: FailureClassMissingEvidence, want: "ask_user"},
		{name: "human reject", fc: FailureClassHumanReject, want: "ask_user"},
		{name: "transient", fc: FailureClassTransient, want: "retry_evaluation"},
		{name: "inconclusive", fc: FailureClassInconclusive, want: "ask_user"},
		{name: "invalid input", fc: FailureClassInvalidInput, want: "ask_user"},
		{name: "user blocked", fc: FailureClassUserBlocked, want: "blocked"},
		{name: "unsafe or out of scope", fc: FailureClassUnsafeOrOutOfScope, want: "blocked"},
		{name: "none", fc: FailureClassNone, want: "finish"},
		{name: "empty", fc: "", want: "finish"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextActionForFailure(tt.fc); got != tt.want {
				t.Fatalf("NextActionForFailure(%q) = %q, want %q", tt.fc, got, tt.want)
			}
		})
	}
}
