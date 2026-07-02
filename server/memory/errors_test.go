package memory

import (
	"errors"
	"testing"
)

// TestRunResolve_TypedErrors confirms the shared resolve entry point returns
// errors that surfaces can classify with errors.Is, not by message text.
func TestRunResolve_TypedErrors(t *testing.T) {
	useTempVault(t)

	// Missing --pair / --all is a validation error.
	if _, err := RunResolve(ResolveSpec{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty spec err = %v, want ErrValidation", err)
	}

	// Apply without a human actor is a validation error.
	_, err := RunResolve(ResolveSpec{All: true, Apply: true, MaxPairs: 20})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("apply without actor err = %v, want ErrValidation", err)
	}

	// A --pair whose members do not exist is a member-not-active error.
	_, err = RunResolve(ResolveSpec{Pair: [2]string{"mem_missing_a", "mem_missing_b"}})
	if !errors.Is(err, ErrMemberNotActive) {
		t.Fatalf("missing pair err = %v, want ErrMemberNotActive", err)
	}
}

// TestUnsupersede_TypedNotFound confirms unsupersede paths wrap ErrNotFound so
// the API maps a missing id to 404 by kind.
func TestUnsupersede_TypedNotFound(t *testing.T) {
	useTempVault(t)
	if _, err := PlanUnsupersede("mem_does_not_exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PlanUnsupersede err = %v, want ErrNotFound", err)
	}
	actor := PromotionActor{Kind: "human", Name: "op"}
	if _, err := Unsupersede("mem_does_not_exist", "reason", actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Unsupersede err = %v, want ErrNotFound", err)
	}
}
