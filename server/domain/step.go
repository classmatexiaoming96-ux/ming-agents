package domain

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StepStatus represents the status of a Step.
type StepStatus string

const (
	StepStatusPending          StepStatus = "pending"
	StepStatusRunning          StepStatus = "running"
	StepStatusCompleted       StepStatus = "completed"
	StepStatusSkipped          StepStatus = "skipped"
	StepStatusFailed           StepStatus = "failed"
	StepStatusWaitingUserInput StepStatus = "waiting_user_input"
)

// StepType represents the type of a step.
type StepType string

const (
	StepTypeTask        StepType = "task"
	StepTypeLoop        StepType = "loop"
	StepTypeConditional StepType = "conditional"
	StepTypeInput       StepType = "input"
)

// Step is a DAG node within a Run.
type Step struct {
	ID            uuid.UUID      `json:"id"`
	RunID         uuid.UUID      `json:"run_id"`
	Name          string         `json:"name"`
	StepType      StepType       `json:"step_type"`
	AdapterKey    string         `json:"adapter_key,omitempty"`
	Status        StepStatus     `json:"status"`
	Iteration     int            `json:"iteration"`
	Attempt       int            `json:"attempt"`
	When          sql.NullString `json:"-"`
	InputsJSON    sql.NullString `json:"-"`
	InputsMap     map[string]any `json:"inputs,omitempty"`
	OutputsJSON   sql.NullString `json:"-"`
	OutputsMap    map[string]any `json:"outputs,omitempty"`
	SkipReason    sql.NullString `json:"-"`
	SkipReasonStr string         `json:"skip_reason,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// MarshalJSON custom marshaler.
func (s Step) MarshalJSON() ([]byte, error) {
	type Alias Step
	aux := struct {
		Alias
		SkipReasonStr string `json:"skip_reason,omitempty"`
	}{
		Alias:         Alias(s),
		SkipReasonStr: s.SkipReason.String,
	}
	if s.InputsJSON.Valid {
		_ = json.Unmarshal([]byte(s.InputsJSON.String), &aux.InputsMap)
	}
	if s.OutputsJSON.Valid {
		_ = json.Unmarshal([]byte(s.OutputsJSON.String), &aux.OutputsMap)
	}
	return json.Marshal(aux)
}

// CanStart returns true if the step can transition to running.
func (s Step) CanStart() bool { return s.Status == StepStatusPending }

// CanComplete returns true if the step can complete.
func (s Step) CanComplete() bool { return s.Status == StepStatusRunning }

// CanSkip returns true if the step can be skipped.
func (s Step) CanSkip() bool { return s.Status == StepStatusPending }
