package workflow

import "testing"

func TestRollbackNodeHelpersDefaultRollbackSpec(t *testing.T) {
	tests := []struct {
		kind          NodeKind
		scope         string
		maxAttempts   int
		reuse         SessionReusePolicy
		contract      RollbackAction
		humanReject   RollbackAction
		productDefect RollbackAction
	}{
		{
			kind:        NodeKindClarification,
			scope:       "clarification",
			maxAttempts: 3,
			reuse:       SessionReuseSameSession,
			humanReject: RollbackActionFixClarification,
		},
		{
			kind:        NodeKindPlanning,
			scope:       "planning",
			maxAttempts: 3,
			reuse:       SessionReuseSameSession,
			contract:    RollbackActionRegeneratePlan,
			humanReject: RollbackActionRegeneratePlan,
		},
		{
			kind:          NodeKindDevelopment,
			scope:         "development",
			maxAttempts:   3,
			reuse:         SessionReuseOnHumanReject,
			contract:      RollbackActionRegenerateSubtask,
			humanReject:   RollbackActionRetrySubtask,
			productDefect: RollbackActionRetrySubtask,
		},
		{
			kind:        NodeKindEvaluation,
			scope:       "evaluation",
			maxAttempts: 2,
			reuse:       SessionReuseNewSession,
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			spec := DefaultRollbackSpec(tt.kind)
			if spec.DefaultUnit.Scope != tt.scope {
				t.Fatalf("Scope = %q, want %q", spec.DefaultUnit.Scope, tt.scope)
			}
			if spec.DefaultUnit.MaxAttempts != tt.maxAttempts {
				t.Fatalf("MaxAttempts = %d, want %d", spec.DefaultUnit.MaxAttempts, tt.maxAttempts)
			}
			if spec.DefaultUnit.ReusePolicy != tt.reuse {
				t.Fatalf("ReusePolicy = %q, want %q", spec.DefaultUnit.ReusePolicy, tt.reuse)
			}
			if spec.OnContract != tt.contract {
				t.Fatalf("OnContract = %q, want %q", spec.OnContract, tt.contract)
			}
			if spec.OnHumanReject != tt.humanReject {
				t.Fatalf("OnHumanReject = %q, want %q", spec.OnHumanReject, tt.humanReject)
			}
			if spec.OnProductDefect != tt.productDefect {
				t.Fatalf("OnProductDefect = %q, want %q", spec.OnProductDefect, tt.productDefect)
			}
		})
	}
}

func TestRollbackNodeHelpersBuildRollbackContext(t *testing.T) {
	req := NodeRequest{
		RunID: "run-1",
		Spec:  NodeSpec{ID: "planning", Kind: NodeKindPlanning},
	}

	rctx := BuildRollbackContext(req)
	if rctx.RunID != "run-1" {
		t.Fatalf("RunID = %q, want run-1", rctx.RunID)
	}
	if rctx.NodeID != "planning" {
		t.Fatalf("NodeID = %q, want planning", rctx.NodeID)
	}
	if rctx.NodeKind != NodeKindPlanning {
		t.Fatalf("NodeKind = %q, want planning", rctx.NodeKind)
	}
	if rctx.Unit.Scope != "planning" {
		t.Fatalf("Unit.Scope = %q, want planning", rctx.Unit.Scope)
	}
	if rctx.Budget.MaxAttempts != 3 {
		t.Fatalf("Budget.MaxAttempts = %d, want 3", rctx.Budget.MaxAttempts)
	}
}

func TestRollbackNodeHelpersHumanRejectSignal(t *testing.T) {
	signal := HumanRejectSignal(
		RollbackUnit{Scope: "subtask:api"},
		ReviewDecision{Reason: "add validation", SessionID: "session-dev-api", NodeName: "subtask:api"},
		"dev-1.out.md",
	)

	if signal.FailureClass != FailureClassHumanReject {
		t.Fatalf("FailureClass = %q, want human_reject", signal.FailureClass)
	}
	if signal.Reason != "add validation" {
		t.Fatalf("Reason = %q, want add validation", signal.Reason)
	}
	if signal.SourceNode != "subtask:api" {
		t.Fatalf("SourceNode = %q, want subtask:api", signal.SourceNode)
	}
	if signal.Suggestion != "dev-1.out.md" {
		t.Fatalf("Suggestion = %q, want dev-1.out.md", signal.Suggestion)
	}
}

func TestRollbackNodeHelpersAttemptPathsForRevision(t *testing.T) {
	prompt, out, exit := AttemptPathsForRevision(
		"/tmp/run/development/dev-1.prompt.md",
		"/tmp/run/development/dev-1.out.md",
		"/tmp/run/development/dev-1.exit",
		2,
	)

	if prompt != "/tmp/run/development/dev-1-revision-2.prompt.md" {
		t.Fatalf("prompt = %q", prompt)
	}
	if out != "/tmp/run/development/dev-1-revision-2.out.md" {
		t.Fatalf("out = %q", out)
	}
	if exit != "/tmp/run/development/dev-1-revision-2.exit" {
		t.Fatalf("exit = %q", exit)
	}
}
