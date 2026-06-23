package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
)

// StepStatusChange describes a persisted step status transition.
type StepStatusChange struct {
	RunID     uuid.UUID
	StepID    uuid.UUID
	StepName  string
	From      domain.StepStatus
	To        domain.StepStatus
	Timestamp time.Time
}

// StepStatusNotifier receives step status changes after they are persisted.
type StepStatusNotifier interface {
	OnStepStatusChanged(change StepStatusChange)
}
