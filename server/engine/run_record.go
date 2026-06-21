package engine

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// RunRecord captures the resolved parameters and evaluation results for a Run.
// Epic 2.12: recordable and replayable dynamic decisions.
// It stores what was actually used (post-resolution), not the raw input params.
// Epic 2.13: DegradationAlert included for run timeline visibility.
// Epic 2.14: CriticallyResults for post-run validation.
type RunRecord struct {
	RunID               uuid.UUID           `json:"run_id"`
	TemplateName        string              `json:"template_name"` // e.g., "bugfix"
	Timestamp           time.Time           `json:"timestamp"`
	ResolvedParams      map[string]map[string]any `json:"resolved_params"` // stepName → resolved inputs map
	EvaluatedAssertions []AssertionResult   `json:"evaluated_assertions"`
	EffectiveThresholds map[string]float64 `json:"effective_thresholds"` // threshold name → value
	SkippedSteps        []SkippedStep       `json:"skipped_steps"`
	RunStatus           string              `json:"run_status"`
	TotalSteps          int                 `json:"total_steps"`
	DegradationAlert   *DegradationAlert   `json:"degradation_alert,omitempty"` // Epic 2.13: degradation summary
	CriticallyResults  []CriticallyResult  `json:"critically_results,omitempty"` // Epic 2.14: post-run validation
}

// AssertionResult captures the result of an evaluated assertion on a step.
type AssertionResult struct {
	StepName    string `json:"step_name"`
	Assertion   string `json:"assertion"` // original assertion expression
	Passed      bool   `json:"passed"`
	ActualValue any    `json:"actual_value"`
	Error       string `json:"error,omitempty"`
}

// SkippedStep records a step that was skipped due to a when-condition evaluating to false.
type SkippedStep struct {
	StepName string `json:"step_name"`
	Reason   string `json:"reason"`
}

// RunRecordStore persists and retrieves RunRecords.
// Epic 2.12
type RunRecordStore interface {
	// SaveRecord persists a run record.
	SaveRecord(rec RunRecord) error

	// GetRecord retrieves a run record by run ID.
	GetRecord(runID uuid.UUID) (RunRecord, error)

	// ListRecordsByTemplate returns all records for a given template.
	ListRecordsByTemplate(templateName string) ([]RunRecord, error)

	// ReplayRun returns the record for replaying a run (same as GetRecord).
	ReplayRun(runID uuid.UUID) (RunRecord, error)
}

// inMemoryRunRecordStore is a thread-safe in-memory implementation of RunRecordStore.
type inMemoryRunRecordStore struct {
	mu      sync.RWMutex
	records map[uuid.UUID]RunRecord
}

// NewInMemoryRunRecordStore creates a new in-memory run record store.
func NewInMemoryRunRecordStore() *inMemoryRunRecordStore {
	return &inMemoryRunRecordStore{
		records: make(map[uuid.UUID]RunRecord),
	}
}

// SaveRecord implements RunRecordStore.
func (s *inMemoryRunRecordStore) SaveRecord(rec RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.RunID] = rec
	return nil
}

// GetRecord implements RunRecordStore.
func (s *inMemoryRunRecordStore) GetRecord(runID uuid.UUID) (RunRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[runID]
	if !ok {
		return RunRecord{}, &RunRecordNotFoundError{RunID: runID}
	}
	return rec, nil
}

// ListRecordsByTemplate implements RunRecordStore.
func (s *inMemoryRunRecordStore) ListRecordsByTemplate(templateName string) ([]RunRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var results []RunRecord
	for _, rec := range s.records {
		if rec.TemplateName == templateName {
			results = append(results, rec)
		}
	}
	return results, nil
}

// ReplayRun implements RunRecordStore.
func (s *inMemoryRunRecordStore) ReplayRun(runID uuid.UUID) (RunRecord, error) {
	return s.GetRecord(runID)
}

// RunRecordNotFoundError is returned when a run record is not found.
type RunRecordNotFoundError struct {
	RunID uuid.UUID
}

func (e *RunRecordNotFoundError) Error() string {
	return "run record not found: " + e.RunID.String()
}