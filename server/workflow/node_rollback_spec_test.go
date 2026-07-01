package workflow

import (
	"context"
	"testing"
)

func TestRollbackNodesUseContextRollbackSpecActions(t *testing.T) {
	tests := []struct {
		name  string
		node  RollbackCapableNode
		kind  NodeKind
		spec  RollbackSpec
		sig   RollbackSignal
		want  RollbackAction
		scope string
	}{
		{
			name: "clarification human reject",
			node: &clarificationNode{},
			kind: NodeKindClarification,
			spec: RollbackSpec{
				DefaultUnit:   RollbackUnit{Scope: "custom-clarification", MaxAttempts: 2, ReusePolicy: SessionReuseNewSession},
				OnHumanReject: RollbackActionAskUser,
			},
			sig:   RollbackSignal{FailureClass: FailureClassHumanReject, Reason: "needs more detail"},
			want:  RollbackActionAskUser,
			scope: "custom-clarification",
		},
		{
			name: "planning contract",
			node: &planningNode{},
			kind: NodeKindPlanning,
			spec: RollbackSpec{
				DefaultUnit: RollbackUnit{Scope: "custom-planning", MaxAttempts: 2, ReusePolicy: SessionReuseNewSession},
				OnContract:  RollbackActionAskUser,
			},
			sig:   RollbackSignal{FailureClass: FailureClassContractError, Reason: "plan schema mismatch"},
			want:  RollbackActionAskUser,
			scope: "custom-planning",
		},
		{
			name: "development product defect",
			node: &developmentNode{},
			kind: NodeKindDevelopment,
			spec: RollbackSpec{
				DefaultUnit:     RollbackUnit{Scope: "custom-development", MaxAttempts: 2, ReusePolicy: SessionReuseNewSession},
				OnProductDefect: RollbackActionRegenerateSubtask,
			},
			sig:   RollbackSignal{FailureClass: FailureClassProductDefect, Reason: "generated wrong feature"},
			want:  RollbackActionRegenerateSubtask,
			scope: "custom-development",
		},
		{
			name: "review contract",
			node: &reviewNode{},
			kind: NodeKindReview,
			spec: RollbackSpec{
				DefaultUnit: RollbackUnit{Scope: "custom-review", MaxAttempts: 2, ReusePolicy: SessionReuseNewSession},
				OnContract:  RollbackActionAskUser,
			},
			sig:   RollbackSignal{FailureClass: FailureClassContractError, Reason: "review report malformed"},
			want:  RollbackActionAskUser,
			scope: "custom-review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := tt.node.PrepareRollback(context.Background(), RollbackContext{
				RunID:    "run-custom",
				NodeID:   string(tt.kind),
				NodeKind: tt.kind,
				Spec:     tt.spec,
				Unit:     tt.spec.DefaultUnit,
			}, tt.sig)
			if err != nil {
				t.Fatalf("PrepareRollback() error = %v", err)
			}
			if decision.Action != tt.want {
				t.Fatalf("Action = %q, want %q", decision.Action, tt.want)
			}
			if decision.TargetScope != tt.scope {
				t.Fatalf("TargetScope = %q, want %q", decision.TargetScope, tt.scope)
			}
		})
	}
}
