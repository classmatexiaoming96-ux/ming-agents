package memory

import "testing"

// allPromotionStates is the closed set the state machine understands, used to
// drive the exhaustive transition matrix below.
var allPromotionStates = []PromotionState{
	PromotionCandidate,
	PromotionUnderReview,
	PromotionPromoted,
	PromotionArchived,
	PromotionSuperseded,
	PromotionRejected,
}

// TestPromotionTransitionMatrix exhaustively covers every (from, to) pair over
// the closed state set. The legal edges are declared here independently of the
// production promotionTransitions map, so adding or removing an edge in the
// source without updating this matrix fails the test — the whole point of a
// table-driven state machine guard is to make silent drift impossible.
func TestPromotionTransitionMatrix(t *testing.T) {
	// legal[from][to] = true means from -> to is an allowed workflow edge.
	legal := map[PromotionState]map[PromotionState]bool{
		PromotionCandidate: {
			PromotionUnderReview: true,
			PromotionRejected:    true,
		},
		PromotionUnderReview: {
			PromotionCandidate: true,
			PromotionRejected:  true,
			PromotionPromoted:  true,
		},
		PromotionPromoted: {
			PromotionArchived:   true,
			PromotionSuperseded: true,
		},
		PromotionSuperseded: {
			PromotionPromoted: true,
		},
		PromotionArchived: {
			PromotionCandidate: true,
		},
		PromotionRejected: {
			PromotionCandidate: true,
		},
	}

	for _, from := range allPromotionStates {
		for _, to := range allPromotionStates {
			from, to := from, to
			wantLegal := legal[from][to]
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				if got := CanTransitionPromotion(from, to); got != wantLegal {
					t.Errorf("CanTransitionPromotion(%q,%q) = %v, want %v", from, to, got, wantLegal)
				}
				err := ValidatePromotionTransition(from, to)
				if wantLegal && err != nil {
					t.Errorf("ValidatePromotionTransition(%q,%q) = %v, want nil", from, to, err)
				}
				if !wantLegal && err == nil {
					t.Errorf("ValidatePromotionTransition(%q,%q) = nil, want error", from, to)
				}
			})
		}
	}
}

// TestPromotionTransition_NoOpNeverLegal confirms a same-state edge is never a
// transition for any state (from == to must fail).
func TestPromotionTransition_NoOpNeverLegal(t *testing.T) {
	for _, s := range allPromotionStates {
		if CanTransitionPromotion(s, s) {
			t.Errorf("CanTransitionPromotion(%q,%q) = true, want false (no-op is not a transition)", s, s)
		}
		if err := ValidatePromotionTransition(s, s); err == nil {
			t.Errorf("ValidatePromotionTransition(%q,%q) = nil, want error", s, s)
		}
	}
}

// TestValidatePromotionTransition_UnknownStatesMatrix covers unknown states on
// either side against every known state.
func TestValidatePromotionTransition_UnknownStatesMatrix(t *testing.T) {
	const bogus PromotionState = "bogus"
	if IsValidPromotionState(bogus) {
		t.Fatal("bogus must not be a valid state")
	}
	for _, s := range allPromotionStates {
		if err := ValidatePromotionTransition(bogus, s); err == nil {
			t.Errorf("ValidatePromotionTransition(bogus,%q) = nil, want error", s)
		}
		if err := ValidatePromotionTransition(s, bogus); err == nil {
			t.Errorf("ValidatePromotionTransition(%q,bogus) = nil, want error", s)
		}
	}
}

// TestIsValidPromotionState_ClosedSet confirms every declared state validates
// and nothing outside the set does.
func TestIsValidPromotionState_ClosedSet(t *testing.T) {
	for _, s := range allPromotionStates {
		if !IsValidPromotionState(s) {
			t.Errorf("IsValidPromotionState(%q) = false, want true", s)
		}
	}
	for _, s := range []PromotionState{"", "bogus", "PROMOTED", "candidate ", "l2"} {
		if IsValidPromotionState(s) {
			t.Errorf("IsValidPromotionState(%q) = true, want false", s)
		}
	}
}

// TestIsRecallVisiblePromotionState_Matrix covers visibility across every state
// and both the l2_inbox and authoritative layer namespaces.
func TestIsRecallVisiblePromotionState_Matrix(t *testing.T) {
	for _, s := range allPromotionStates {
		// l2_inbox is never recall-visible regardless of state.
		if IsRecallVisiblePromotionState(s, "l2_inbox") {
			t.Errorf("l2_inbox state %q must never be recall-visible", s)
		}
		// Outside l2_inbox, only promoted is visible.
		wantVisible := s == PromotionPromoted
		for _, layer := range []string{"", "l2", "l1"} {
			if got := IsRecallVisiblePromotionState(s, layer); got != wantVisible {
				t.Errorf("IsRecallVisiblePromotionState(%q, %q) = %v, want %v", s, layer, got, wantVisible)
			}
		}
	}
}

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
