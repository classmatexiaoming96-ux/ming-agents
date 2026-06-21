package engine

import (
	"github.com/google/uuid"
)

// StepInfo is an internal step representation used by the engine.
// It's a lightweight view of domain.Step for engine operations.
type Step struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	StepType  string    `json:"step_type"`
	When      *string   `json:"when,omitempty"`
	InputsMap map[string]any `json:"inputs,omitempty"`
}