package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/ming-agents/server/memory"
)

// TestResolveErrStatus_TypedMapping asserts each typed memory error maps to the
// documented HTTP status by kind (errors.Is), independent of message wording.
// Wrapping each sentinel confirms the mapping survives added context text.
func TestResolveErrStatus_TypedMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", memory.ErrNotFound, http.StatusNotFound},
		{"member not active", memory.ErrMemberNotActive, http.StatusNotFound},
		{"not pending contradiction", memory.ErrNotPendingContradiction, http.StatusUnprocessableEntity},
		{"unbounded batch", memory.ErrUnboundedBatch, http.StatusUnprocessableEntity},
		{"invalid transition", memory.ErrInvalidTransition, http.StatusConflict},
		{"l1 supersede", memory.ErrL1Supersede, http.StatusConflict},
		{"validation", memory.ErrValidation, http.StatusBadRequest},
		{"unknown", fmt.Errorf("some unexpected failure"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// bare sentinel
			if got := resolveErrStatus(tc.err); got != tc.want {
				t.Errorf("resolveErrStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
			// wrapped with extra context must still classify by kind
			wrapped := fmt.Errorf("context around: %w", tc.err)
			if got := resolveErrStatus(wrapped); got != tc.want {
				t.Errorf("resolveErrStatus(wrapped %v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
