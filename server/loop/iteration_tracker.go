package loop

import (
	"sync"
	"time"
)

// IterationRecord records the state of a single loop iteration.
// Epic 3.5: IterationRecord tracks iteration ↔ Task association, scores, and deltas.
type IterationRecord struct {
	Iteration  int            `json:"iteration"`
	TaskIDs    []string       `json:"task_ids"`              // which tasks ran in this iteration
	Score      float64        `json:"score"`                 // evaluation score for this iteration
	ScoreDelta float64        `json:"score_delta"`           // change from previous iteration
	Feedback   string         `json:"feedback"`              // feedback text from evaluation
	Outputs    map[string]any `json:"outputs"`              // outputs from this iteration
	Converged  bool           `json:"converged"`             // whether convergence was achieved
	Timestamp  time.Time      `json:"timestamp"`             // when this iteration completed
}

// IterationTracker interface for tracking iteration records.
// Epic 3.5: Provides iteration record persistence and retrieval.
type IterationTracker interface {
	// RecordIteration saves an iteration record for a run.
	RecordIteration(runID string, rec IterationRecord)
	// GetIterationRecords returns all iteration records for a run, ordered by iteration.
	GetIterationRecords(runID string) []IterationRecord
	// GetIterationRecord returns a specific iteration record.
	GetIterationRecord(runID string, iter int) IterationRecord
	// GetLatestIteration returns the most recent iteration record.
	GetLatestIteration(runID string) IterationRecord
	// ClearRunRecords removes all iteration records for a run.
	ClearRunRecords(runID string)
}

// InMemoryIterationTracker is a thread-safe in-memory implementation of IterationTracker.
// Suitable for single-node deployments and testing.
type InMemoryIterationTracker struct {
	mu       sync.RWMutex
	records  map[string][]IterationRecord // keyed by runID
}

// NewInMemoryIterationTracker creates a new in-memory iteration tracker.
func NewInMemoryIterationTracker() *InMemoryIterationTracker {
	return &InMemoryIterationTracker{
		records: make(map[string][]IterationRecord),
	}
}

// RecordIteration saves an iteration record for a run.
func (t *InMemoryIterationTracker) RecordIteration(runID string, rec IterationRecord) {
	if runID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records[runID] = append(t.records[runID], rec)
}

// GetIterationRecords returns all iteration records for a run, ordered by iteration.
func (t *InMemoryIterationTracker) GetIterationRecords(runID string) []IterationRecord {
	if runID == "" {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	recs, ok := t.records[runID]
	if !ok {
		return []IterationRecord{}
	}
	// Return a copy to avoid race conditions
	result := make([]IterationRecord, len(recs))
	copy(result, recs)
	return result
}

// GetIterationRecord returns a specific iteration record.
func (t *InMemoryIterationTracker) GetIterationRecord(runID string, iter int) IterationRecord {
	recs := t.GetIterationRecords(runID)
	for _, rec := range recs {
		if rec.Iteration == iter {
			return rec
		}
	}
	return IterationRecord{}
}

// GetLatestIteration returns the most recent iteration record.
func (t *InMemoryIterationTracker) GetLatestIteration(runID string) IterationRecord {
	recs := t.GetIterationRecords(runID)
	if len(recs) == 0 {
		return IterationRecord{}
	}
	return recs[len(recs)-1]
}

// ClearRunRecords removes all iteration records for a run.
func (t *InMemoryIterationTracker) ClearRunRecords(runID string) {
	if runID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.records, runID)
}

// Len returns the number of iteration records for a run.
func (t *InMemoryIterationTracker) Len(runID string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.records[runID])
}

// GetScoreHistory returns the score history for a run.
// Returns an empty slice if no records exist.
func GetScoreHistory(tracker IterationTracker, runID string) []float64 {
	recs := tracker.GetIterationRecords(runID)
	if len(recs) == 0 {
		return []float64{}
	}
	scores := make([]float64, len(recs))
	for i, rec := range recs {
		scores[i] = rec.Score
	}
	return scores
}

// GetProgressTrend analyzes score history and returns trend direction.
// Returns: "improving", "declining", or "stagnant"
// Epic 3.5: Helper method to determine if loop is making progress.
func GetProgressTrend(tracker IterationTracker, runID string) string {
	recs := tracker.GetIterationRecords(runID)
	if len(recs) < 2 {
		return "stagnant"
	}

	// Calculate average delta over recent iterations
	var totalDelta float64
	validDeltas := 0
	for i := 1; i < len(recs); i++ {
		delta := recs[i].Score - recs[i-1].Score
		totalDelta += delta
		validDeltas++
	}

	if validDeltas == 0 {
		return "stagnant"
	}

	avgDelta := totalDelta / float64(validDeltas)

	// Threshold for determining significant change
	const threshold = 0.01

	if avgDelta > threshold {
		return "improving"
	} else if avgDelta < -threshold {
		return "declining"
	}
	return "stagnant"
}

// MockIterationTracker is a test-friendly IterationTracker with programmable responses.
type MockIterationTracker struct {
	Records         []IterationRecord
	RecordIterationFunc func(runID string, rec IterationRecord)
	GetRecordsFunc  func(runID string) []IterationRecord
}

// RecordIteration saves an iteration record.
func (m *MockIterationTracker) RecordIteration(runID string, rec IterationRecord) {
	if m.RecordIterationFunc != nil {
		m.RecordIterationFunc(runID, rec)
		return
	}
	m.Records = append(m.Records, rec)
}

// GetIterationRecords returns all iteration records.
func (m *MockIterationTracker) GetIterationRecords(runID string) []IterationRecord {
	if m.GetRecordsFunc != nil {
		return m.GetRecordsFunc(runID)
	}
	if m.Records == nil {
		return []IterationRecord{}
	}
	return m.Records
}

// GetIterationRecord returns a specific iteration record.
func (m *MockIterationTracker) GetIterationRecord(runID string, iter int) IterationRecord {
	recs := m.GetIterationRecords(runID)
	for _, rec := range recs {
		if rec.Iteration == iter {
			return rec
		}
	}
	return IterationRecord{}
}

// GetLatestIteration returns the most recent iteration record.
func (m *MockIterationTracker) GetLatestIteration(runID string) IterationRecord {
	recs := m.GetIterationRecords(runID)
	if len(recs) == 0 {
		return IterationRecord{}
	}
	return recs[len(recs)-1]
}

// ClearRunRecords clears all records.
func (m *MockIterationTracker) ClearRunRecords(runID string) {
	m.Records = []IterationRecord{}
}