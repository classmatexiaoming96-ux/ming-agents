package engine

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// DegradationSeverity represents the severity level of a degradation event.
type DegradationSeverity string

const (
	SeverityInfo     DegradationSeverity = "info"
	SeverityWarning  DegradationSeverity = "warning"
	SeverityError    DegradationSeverity = "error"
)

// DegradationEvent records a parameter fallback event during run execution.
// Epic 2.13: degradation reporting mechanism.
// A degradation occurs when a parameter value differs from what was requested,
// e.g., "model X was unavailable → fell back to model Y".
type DegradationEvent struct {
	EventID        uuid.UUID           `json:"event_id"`
	RunID          uuid.UUID           `json:"run_id"`
	Timestamp      time.Time           `json:"timestamp"`
	Reason         string              `json:"reason"`                   // e.g., "model_unavailable", "rate_limit", "timeout"
	TriggeredParam string             `json:"triggered_param"`          // which parameter triggered the fallback
	OriginalValue  any                 `json:"original_value"`           // the originally requested value
	FallbackValue  any                 `json:"fallback_value"`          // the actual value used
	Severity       DegradationSeverity `json:"severity"`                // info, warning, error
	Message        string              `json:"message"`                 // human-readable description
	StepName       string              `json:"step_name,omitempty"`     // the step where degradation occurred
}

// DegradationAlert aggregates degradation events for a run for alerting purposes.
type DegradationAlert struct {
	RunID            uuid.UUID           `json:"run_id"`
	DegradationCount int                 `json:"degradation_count"`
	MostSevere       DegradationSeverity `json:"most_severe"`      // highest severity among all events
	DegradationList  []DegradationEvent   `json:"degradation_list"` // all events for this run
}

// DegradationReporter is the interface for reporting degradation events.
// Implemented by components that need to report parameter fallbacks.
type DegradationReporter interface {
	// ReportDegradation records a degradation event.
	ReportDegradation(evt DegradationEvent) error

	// GetDegradationsByRun returns all degradation events for a given run.
	GetDegradationsByRun(runID uuid.UUID) ([]DegradationEvent, error)

	// GetDegradationCount returns the number of degradation events for a run.
	GetDegradationCount(runID uuid.UUID) int
}

// DegradationStore is the interface for persisting degradation events.
// Similar to RunRecordStore but specific to degradation data.
type DegradationStore interface {
	// SaveDegradation persists a degradation event.
	SaveDegradation(evt DegradationEvent) error

	// GetDegradationsByRun returns all degradation events for a run.
	GetDegradationsByRun(runID uuid.UUID) ([]DegradationEvent, error)

	// GetDegradationCount returns the count of degradation events for a run.
	GetDegradationCount(runID uuid.UUID) int

	// GetDegradationAlert returns an aggregated alert for a run.
	GetDegradationAlert(runID uuid.UUID) (DegradationAlert, error)
}

// inMemoryDegradationStore is a thread-safe in-memory implementation of DegradationStore.
type inMemoryDegradationStore struct {
	mu     sync.RWMutex
	events map[uuid.UUID][]DegradationEvent // runID → events
}

// NewInMemoryDegradationStore creates a new in-memory degradation store.
func NewInMemoryDegradationStore() *inMemoryDegradationStore {
	return &inMemoryDegradationStore{
		events: make(map[uuid.UUID][]DegradationEvent),
	}
}

// SaveDegradation implements DegradationStore.
func (s *inMemoryDegradationStore) SaveDegradation(evt DegradationEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[evt.RunID] = append(s.events[evt.RunID], evt)
	return nil
}

// GetDegradationsByRun implements DegradationStore.
func (s *inMemoryDegradationStore) GetDegradationsByRun(runID uuid.UUID) ([]DegradationEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events, ok := s.events[runID]
	if !ok {
		return []DegradationEvent{}, nil
	}
	// Return a copy to prevent external mutation.
	result := make([]DegradationEvent, len(events))
	copy(result, events)
	return result, nil
}

// GetDegradationCount implements DegradationStore.
func (s *inMemoryDegradationStore) GetDegradationCount(runID uuid.UUID) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events[runID])
}

// GetDegradationAlert implements DegradationStore.
func (s *inMemoryDegradationStore) GetDegradationAlert(runID uuid.UUID) (DegradationAlert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events, ok := s.events[runID]
	if !ok {
		return DegradationAlert{RunID: runID}, nil
	}

	// Find the most severe event.
	mostSevere := SeverityInfo
	for _, evt := range events {
		if evt.Severity == SeverityError {
			mostSevere = SeverityError
			break // Can't get worse than error
		} else if evt.Severity == SeverityWarning && mostSevere != SeverityError {
			mostSevere = SeverityWarning
		}
	}

	// Return a copy of events to prevent external mutation.
	eventCopy := make([]DegradationEvent, len(events))
	copy(eventCopy, events)

	return DegradationAlert{
		RunID:            runID,
		DegradationCount: len(events),
		MostSevere:       mostSevere,
		DegradationList:  eventCopy,
	}, nil
}

// SeverityOrder returns a numeric weight for severity comparison.
// Higher number = more severe.
func SeverityOrder(s DegradationSeverity) int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// MostSevereSeverity returns the more severe of two severities.
func MostSevereSeverity(a, b DegradationSeverity) DegradationSeverity {
	if SeverityOrder(a) >= SeverityOrder(b) {
		return a
	}
	return b
}

// DegradationNotFoundError is returned when degradation events are not found for a run.
type DegradationNotFoundError struct {
	RunID uuid.UUID
}

func (e *DegradationNotFoundError) Error() string {
	return "degradation events not found for run: " + e.RunID.String()
}