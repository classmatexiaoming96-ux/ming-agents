package memory

import "fmt"

// PromotionState tracks where a memory sits in the Phase 7 authority workflow.
// It is deliberately separate from Status (the recall lifecycle) so review
// intent never overloads whether a memory is active in recall.
type PromotionState string

const (
	// PromotionCandidate is the default for imported or generated content that
	// has not been accepted into its target authority layer.
	PromotionCandidate PromotionState = "candidate"
	// PromotionUnderReview means a human or review job has opened a decision.
	PromotionUnderReview PromotionState = "under_review"
	// PromotionPromoted means the item was accepted into its target layer.
	PromotionPromoted PromotionState = "promoted"
	// PromotionArchived means the item is retired but preserved for history.
	PromotionArchived PromotionState = "archived"
	// PromotionSuperseded means another memory replaces it.
	PromotionSuperseded PromotionState = "superseded"
	// PromotionRejected means a candidate or reviewed item was declined.
	PromotionRejected PromotionState = "rejected"
)

// validPromotionStates is the closed set of states the workflow understands.
var validPromotionStates = map[PromotionState]bool{
	PromotionCandidate:   true,
	PromotionUnderReview: true,
	PromotionPromoted:    true,
	PromotionArchived:    true,
	PromotionSuperseded:  true,
	PromotionRejected:    true,
}

// IsValidPromotionState reports whether s is a recognised workflow state.
func IsValidPromotionState(s PromotionState) bool {
	return validPromotionStates[s]
}

// promotionTransitions encodes the valid state-machine edges from the design.
// No direct candidate -> promoted edge exists: even strong evidence must pass an
// explicit review before promotion.
var promotionTransitions = map[PromotionState]map[PromotionState]bool{
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

// CanTransitionPromotion reports whether from -> to is a legal workflow edge.
// A no-op (from == to) is not a transition and returns false.
func CanTransitionPromotion(from, to PromotionState) bool {
	return promotionTransitions[from][to]
}

// ValidatePromotionTransition returns nil when from -> to is legal and a
// descriptive error otherwise, so callers get a stable failure reason for audit.
func ValidatePromotionTransition(from, to PromotionState) error {
	if !IsValidPromotionState(from) {
		return fmt.Errorf("invalid source promotion state %q", from)
	}
	if !IsValidPromotionState(to) {
		return fmt.Errorf("invalid target promotion state %q", to)
	}
	if !CanTransitionPromotion(from, to) {
		return fmt.Errorf("promotion transition %q -> %q is not allowed", from, to)
	}
	return nil
}

// ResolvePromotionState returns the effective promotion state for a memory,
// applying read-time defaulting for pre-Phase-7 files that carry no explicit
// promotion_state. This is preferred over a destructive migration: existing
// project memories keep their authority without any rewrite.
//
// Defaulting rules (from the compatibility chapter):
//   - explicit, recognised state on disk wins;
//   - superseded status -> superseded;
//   - archived status -> archived;
//   - l2_inbox layer -> candidate (cross-project items stay non-authoritative);
//   - otherwise active l2/legacy notes -> promoted.
func ResolvePromotionState(m Memory) PromotionState {
	if IsValidPromotionState(m.PromotionState) {
		return m.PromotionState
	}
	switch m.Status {
	case "superseded":
		return PromotionSuperseded
	case "archived":
		return PromotionArchived
	}
	if m.Layer == "l2_inbox" {
		return PromotionCandidate
	}
	return PromotionPromoted
}

// IsRecallVisiblePromotionState reports whether a memory in the given promotion
// state and layer should be visible to authoritative recall. Only promoted
// authority memories surface; candidates, review, rejected, archived, and
// superseded items (and the l2_inbox namespace) are excluded.
func IsRecallVisiblePromotionState(state PromotionState, layer string) bool {
	if layer == "l2_inbox" {
		return false
	}
	return state == PromotionPromoted
}
