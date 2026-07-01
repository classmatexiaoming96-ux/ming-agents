package memory

import "testing"

func TestPromotionTransitions_ValidEdges(t *testing.T) {
	valid := []struct{ from, to PromotionState }{
		{PromotionCandidate, PromotionUnderReview},
		{PromotionCandidate, PromotionRejected},
		{PromotionUnderReview, PromotionCandidate},
		{PromotionUnderReview, PromotionRejected},
		{PromotionUnderReview, PromotionPromoted},
		{PromotionPromoted, PromotionArchived},
		{PromotionPromoted, PromotionSuperseded},
		{PromotionSuperseded, PromotionPromoted},
		{PromotionArchived, PromotionCandidate},
		{PromotionRejected, PromotionCandidate},
	}
	for _, tt := range valid {
		if err := ValidatePromotionTransition(tt.from, tt.to); err != nil {
			t.Errorf("ValidatePromotionTransition(%q,%q) = %v, want nil", tt.from, tt.to, err)
		}
	}
}

func TestPromotionTransitions_RejectsDirectCandidateToPromoted(t *testing.T) {
	if err := ValidatePromotionTransition(PromotionCandidate, PromotionPromoted); err == nil {
		t.Fatal("candidate -> promoted must be rejected; L1/L2 authority requires review first")
	}
}

func TestPromotionTransitions_RejectsInvalidEdges(t *testing.T) {
	invalid := []struct{ from, to PromotionState }{
		{PromotionCandidate, PromotionPromoted},
		{PromotionCandidate, PromotionArchived},
		{PromotionCandidate, PromotionSuperseded},
		{PromotionPromoted, PromotionCandidate},
		{PromotionPromoted, PromotionUnderReview},
		{PromotionArchived, PromotionPromoted},
		{PromotionRejected, PromotionPromoted},
		{PromotionCandidate, PromotionCandidate}, // no-op is not a transition
	}
	for _, tt := range invalid {
		if err := ValidatePromotionTransition(tt.from, tt.to); err == nil {
			t.Errorf("ValidatePromotionTransition(%q,%q) = nil, want error", tt.from, tt.to)
		}
	}
}

func TestValidatePromotionTransition_RejectsUnknownStates(t *testing.T) {
	if err := ValidatePromotionTransition("bogus", PromotionPromoted); err == nil {
		t.Fatal("unknown source state must be rejected")
	}
	if err := ValidatePromotionTransition(PromotionCandidate, "bogus"); err == nil {
		t.Fatal("unknown target state must be rejected")
	}
}

func TestResolvePromotionState_ReadTimeDefaulting(t *testing.T) {
	tests := []struct {
		name string
		mem  Memory
		want PromotionState
	}{
		{
			name: "explicit state wins",
			mem:  Memory{PromotionState: PromotionUnderReview, Status: "active", Layer: "l2"},
			want: PromotionUnderReview,
		},
		{
			name: "legacy active l2 defaults to promoted",
			mem:  Memory{Status: "active", Layer: "l2"},
			want: PromotionPromoted,
		},
		{
			name: "legacy active no-layer defaults to promoted",
			mem:  Memory{Status: "active"},
			want: PromotionPromoted,
		},
		{
			name: "inbox candidate stays candidate",
			mem:  Memory{Status: "active", Layer: "l2_inbox"},
			want: PromotionCandidate,
		},
		{
			name: "archived status defaults to archived",
			mem:  Memory{Status: "archived", Layer: "l2"},
			want: PromotionArchived,
		},
		{
			name: "superseded status defaults to superseded",
			mem:  Memory{Status: "superseded", Layer: "l2"},
			want: PromotionSuperseded,
		},
		{
			name: "unrecognised on-disk state falls through to defaulting",
			mem:  Memory{PromotionState: "bogus", Status: "active", Layer: "l2"},
			want: PromotionPromoted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolvePromotionState(tt.mem); got != tt.want {
				t.Fatalf("ResolvePromotionState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsRecallVisiblePromotionState(t *testing.T) {
	if !IsRecallVisiblePromotionState(PromotionPromoted, "l2") {
		t.Fatal("promoted l2 memory must be recall-visible")
	}
	if IsRecallVisiblePromotionState(PromotionPromoted, "l2_inbox") {
		t.Fatal("l2_inbox is never recall-visible even when promoted")
	}
	for _, s := range []PromotionState{PromotionCandidate, PromotionUnderReview, PromotionRejected, PromotionArchived, PromotionSuperseded} {
		if IsRecallVisiblePromotionState(s, "l2") {
			t.Fatalf("state %q must not be recall-visible", s)
		}
	}
}
