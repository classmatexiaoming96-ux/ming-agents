package loop

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// FeedbackStore persists and retrieves iteration snapshots.
// Epic 3.3: FeedbackStore manages iteration history for feedback assembly.
type FeedbackStore interface {
	// SaveSnapshot saves an iteration snapshot.
	SaveSnapshot(runID string, snap *IterationSnapshot) error
	// GetSnapshots returns all snapshots for a run, ordered by iteration.
	GetSnapshots(runID string) ([]*IterationSnapshot, error)
	// GetLatestSnapshot returns the most recent snapshot for a run.
	GetLatestSnapshot(runID string) (*IterationSnapshot, error)
	// GetSnapshotsByStep returns all snapshots for a specific step in a run.
	GetSnapshotsByStep(runID string, stepID uuid.UUID) ([]*IterationSnapshot, error)
	// GetSnapshotByIteration returns a specific iteration snapshot.
	GetSnapshotByIteration(runID string, stepID uuid.UUID, iteration int) (*IterationSnapshot, error)
	// ClearRunSnapshots removes all snapshots for a run.
	ClearRunSnapshots(runID string) error
}

// InMemoryFeedbackStore is a thread-safe in-memory implementation of FeedbackStore.
// Suitable for single-node deployments and testing.
type InMemoryFeedbackStore struct {
	mu       sync.RWMutex
	snapshots map[string][]*IterationSnapshot // keyed by runID
}

// NewInMemoryFeedbackStore creates a new in-memory feedback store.
func NewInMemoryFeedbackStore() *InMemoryFeedbackStore {
	return &InMemoryFeedbackStore{
		snapshots: make(map[string][]*IterationSnapshot),
	}
}

// SaveSnapshot saves an iteration snapshot.
func (s *InMemoryFeedbackStore) SaveSnapshot(runID string, snap *IterationSnapshot) error {
	if runID == "" {
		return fmt.Errorf("runID cannot be empty")
	}
	if snap == nil {
		return fmt.Errorf("snapshot cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshots[runID] = append(s.snapshots[runID], snap)
	return nil
}

// GetSnapshots returns all snapshots for a run, ordered by iteration.
func (s *InMemoryFeedbackStore) GetSnapshots(runID string) ([]*IterationSnapshot, error) {
	if runID == "" {
		return nil, fmt.Errorf("runID cannot be empty")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	snaps, ok := s.snapshots[runID]
	if !ok {
		return []*IterationSnapshot{}, nil
	}

	// Return a copy to avoid race conditions
	result := make([]*IterationSnapshot, len(snaps))
	copy(result, snaps)
	return result, nil
}

// GetLatestSnapshot returns the most recent snapshot for a run.
func (s *InMemoryFeedbackStore) GetLatestSnapshot(runID string) (*IterationSnapshot, error) {
	snaps, err := s.GetSnapshots(runID)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return nil, nil
	}
	return snaps[len(snaps)-1], nil
}

// GetSnapshotsByStep returns all snapshots for a specific step in a run.
func (s *InMemoryFeedbackStore) GetSnapshotsByStep(runID string, stepID uuid.UUID) ([]*IterationSnapshot, error) {
	snaps, err := s.GetSnapshots(runID)
	if err != nil {
		return nil, err
	}

	var result []*IterationSnapshot
	for _, snap := range snaps {
		if snap.StepID == stepID {
			result = append(result, snap)
		}
	}
	return result, nil
}

// GetSnapshotByIteration returns a specific iteration snapshot.
func (s *InMemoryFeedbackStore) GetSnapshotByIteration(runID string, stepID uuid.UUID, iteration int) (*IterationSnapshot, error) {
	snaps, err := s.GetSnapshotsByStep(runID, stepID)
	if err != nil {
		return nil, err
	}

	for _, snap := range snaps {
		if snap.Iteration == iteration {
			return snap, nil
		}
	}
	return nil, nil
}

// ClearRunSnapshots removes all snapshots for a run.
func (s *InMemoryFeedbackStore) ClearRunSnapshots(runID string) error {
	if runID == "" {
		return fmt.Errorf("runID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.snapshots, runID)
	return nil
}

// Len returns the number of snapshots for a run.
func (s *InMemoryFeedbackStore) Len(runID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.snapshots[runID])
}

// MockFeedbackStore is a test-friendly FeedbackStore with programmable responses.
type MockFeedbackStore struct {
	Snapshots     []*IterationSnapshot
	SaveErr       error
	GetErr        error
	SaveCalls     []SaveCall
	CallCount     int
}

type SaveCall struct {
	RunID string
	Snap  *IterationSnapshot
}

// NewMockFeedbackStore creates a MockFeedbackStore with empty defaults.
func NewMockFeedbackStore() *MockFeedbackStore {
	return &MockFeedbackStore{
		Snapshots: make([]*IterationSnapshot, 0),
		SaveCalls: make([]SaveCall, 0),
	}
}

// SaveSnapshot saves a snapshot and records the call.
func (m *MockFeedbackStore) SaveSnapshot(runID string, snap *IterationSnapshot) error {
	m.CallCount++
	m.SaveCalls = append(m.SaveCalls, SaveCall{RunID: runID, Snap: snap})
	if m.SaveErr != nil {
		return m.SaveErr
	}
	m.Snapshots = append(m.Snapshots, snap)
	return nil
}

// GetSnapshots returns all snapshots.
func (m *MockFeedbackStore) GetSnapshots(runID string) ([]*IterationSnapshot, error) {
	if m.GetErr != nil {
		return nil, m.GetErr
	}
	return m.Snapshots, nil
}

// GetLatestSnapshot returns the last snapshot.
func (m *MockFeedbackStore) GetLatestSnapshot(runID string) (*IterationSnapshot, error) {
	snaps, err := m.GetSnapshots(runID)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return nil, nil
	}
	return snaps[len(snaps)-1], nil
}

// GetSnapshotsByStep returns snapshots for a step.
func (m *MockFeedbackStore) GetSnapshotsByStep(runID string, stepID uuid.UUID) ([]*IterationSnapshot, error) {
	if m.GetErr != nil {
		return nil, m.GetErr
	}
	var result []*IterationSnapshot
	for _, snap := range m.Snapshots {
		if snap.StepID == stepID {
			result = append(result, snap)
		}
	}
	return result, nil
}

// GetSnapshotByIteration returns a specific iteration.
func (m *MockFeedbackStore) GetSnapshotByIteration(runID string, stepID uuid.UUID, iteration int) (*IterationSnapshot, error) {
	snaps, err := m.GetSnapshotsByStep(runID, stepID)
	if err != nil {
		return nil, err
	}
	for _, snap := range snaps {
		if snap.Iteration == iteration {
			return snap, nil
		}
	}
	return nil, nil
}

// ClearRunSnapshots clears all snapshots.
func (m *MockFeedbackStore) ClearRunSnapshots(runID string) error {
	m.Snapshots = make([]*IterationSnapshot, 0)
	return nil
}