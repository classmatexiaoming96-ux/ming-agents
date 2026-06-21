package loop

import (
	"testing"
	"time"
)

// ─── IterationRecord Tests ─────────────────────────────────────────────────────

func TestIterationRecord_Fields(t *testing.T) {
	rec := IterationRecord{
		Iteration:  1,
		TaskIDs:    []string{"task-1", "task-2"},
		Score:      0.85,
		ScoreDelta: 0.15,
		Feedback:   "Good progress",
		Outputs:    map[string]any{"result": "improved"},
		Converged:  false,
		Timestamp:  time.Now(),
	}

	if rec.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", rec.Iteration)
	}
	if len(rec.TaskIDs) != 2 {
		t.Errorf("len(TaskIDs) = %d, want 2", len(rec.TaskIDs))
	}
	if rec.Score != 0.85 {
		t.Errorf("Score = %v, want 0.85", rec.Score)
	}
	if rec.ScoreDelta != 0.15 {
		t.Errorf("ScoreDelta = %v, want 0.15", rec.ScoreDelta)
	}
	if rec.Converged != false {
		t.Errorf("Converged = %v, want false", rec.Converged)
	}
}

// ─── InMemoryIterationTracker Tests ──────────────────────────────────────────

func TestInMemoryIterationTracker_RecordIteration(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-123"

	rec1 := IterationRecord{
		Iteration: 1,
		Score:     0.5,
		Timestamp: time.Now(),
	}
	tracker.RecordIteration(runID, rec1)

	if tracker.Len(runID) != 1 {
		t.Errorf("Len(runID) = %d, want 1", tracker.Len(runID))
	}

	rec2 := IterationRecord{
		Iteration: 2,
		Score:     0.7,
		Timestamp: time.Now(),
	}
	tracker.RecordIteration(runID, rec2)

	if tracker.Len(runID) != 2 {
		t.Errorf("Len(runID) = %d, want 2", tracker.Len(runID))
	}
}

func TestInMemoryIterationTracker_RecordIteration_EmptyRunID(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	rec := IterationRecord{Iteration: 1, Score: 0.5}

	// Should not panic with empty runID
	tracker.RecordIteration("", rec)
	if tracker.Len("") != 0 {
		t.Errorf("Len(\"\") = %d, want 0", tracker.Len(""))
	}
}

func TestInMemoryIterationTracker_GetIterationRecords(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-456"

	// Add records
	for i := 1; i <= 3; i++ {
		tracker.RecordIteration(runID, IterationRecord{
			Iteration: i,
			Score:     float64(i) * 0.2,
			Timestamp: time.Now(),
		})
	}

	recs := tracker.GetIterationRecords(runID)
	if len(recs) != 3 {
		t.Errorf("len(GetIterationRecords()) = %d, want 3", len(recs))
	}

	// Verify order
	for i, rec := range recs {
		if rec.Iteration != i+1 {
			t.Errorf("rec[%d].Iteration = %d, want %d", i, rec.Iteration, i+1)
		}
	}
}

func TestInMemoryIterationTracker_GetIterationRecords_NotFound(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	recs := tracker.GetIterationRecords("nonexistent")
	if recs == nil {
		t.Error("GetIterationRecords(\"nonexistent\") returned nil, want empty slice")
	}
	if len(recs) != 0 {
		t.Errorf("len(GetIterationRecords()) = %d, want 0", len(recs))
	}
}

func TestInMemoryIterationTracker_GetIterationRecord(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-789"

	tracker.RecordIteration(runID, IterationRecord{
		Iteration: 1,
		Score:     0.5,
		TaskIDs:   []string{"task-1"},
		Timestamp: time.Now(),
	})
	tracker.RecordIteration(runID, IterationRecord{
		Iteration: 2,
		Score:     0.7,
		TaskIDs:   []string{"task-2", "task-3"},
		Timestamp: time.Now(),
	})

	rec := tracker.GetIterationRecord(runID, 1)
	if rec.Iteration != 1 {
		t.Errorf("GetIterationRecord(1).Iteration = %d, want 1", rec.Iteration)
	}
	if rec.Score != 0.5 {
		t.Errorf("GetIterationRecord(1).Score = %v, want 0.5", rec.Score)
	}
	if len(rec.TaskIDs) != 1 {
		t.Errorf("len(GetIterationRecord(1).TaskIDs) = %d, want 1", len(rec.TaskIDs))
	}

	rec2 := tracker.GetIterationRecord(runID, 2)
	if rec2.Iteration != 2 {
		t.Errorf("GetIterationRecord(2).Iteration = %d, want 2", rec2.Iteration)
	}
	if len(rec2.TaskIDs) != 2 {
		t.Errorf("len(GetIterationRecord(2).TaskIDs) = %d, want 2", len(rec2.TaskIDs))
	}

	// Non-existent iteration
	rec3 := tracker.GetIterationRecord(runID, 99)
	if rec3.Iteration != 0 {
		t.Errorf("GetIterationRecord(99).Iteration = %d, want 0 (empty record)", rec3.Iteration)
	}
}

func TestInMemoryIterationTracker_GetLatestIteration(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-latest"

	// Add records
	for i := 1; i <= 5; i++ {
		tracker.RecordIteration(runID, IterationRecord{
			Iteration: i,
			Score:     float64(i) * 0.15,
			Timestamp: time.Now(),
		})
	}

	latest := tracker.GetLatestIteration(runID)
	if latest.Iteration != 5 {
		t.Errorf("GetLatestIteration().Iteration = %d, want 5", latest.Iteration)
	}
	if latest.Score != 0.75 {
		t.Errorf("GetLatestIteration().Score = %v, want 0.75", latest.Score)
	}
}

func TestInMemoryIterationTracker_GetLatestIteration_Empty(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	latest := tracker.GetLatestIteration("nonexistent")
	if latest.Iteration != 0 {
		t.Errorf("GetLatestIteration(\"nonexistent\").Iteration = %d, want 0", latest.Iteration)
	}
}

func TestInMemoryIterationTracker_ClearRunRecords(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-clear"

	tracker.RecordIteration(runID, IterationRecord{Iteration: 1, Score: 0.5})
	tracker.RecordIteration(runID, IterationRecord{Iteration: 2, Score: 0.7})

	if tracker.Len(runID) != 2 {
		t.Errorf("before clear: Len = %d, want 2", tracker.Len(runID))
	}

	tracker.ClearRunRecords(runID)
	if tracker.Len(runID) != 0 {
		t.Errorf("after clear: Len = %d, want 0", tracker.Len(runID))
	}
}

func TestInMemoryIterationTracker_ClearRunRecords_EmptyRunID(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	// Should not panic
	tracker.ClearRunRecords("")
}

func TestInMemoryIterationTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-concurrent"

	// Concurrently add records
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			for j := 0; j < 100; j++ {
				tracker.RecordIteration(runID, IterationRecord{
					Iteration: idx*100 + j,
					Score:     float64(idx) * 0.1,
					Timestamp: time.Now(),
				})
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify total count (may be less than 1000 due to race in map access, but should be close)
	count := tracker.Len(runID)
	if count != 1000 {
		t.Errorf("Len after concurrent writes = %d, want 1000", count)
	}
}

// ─── GetScoreHistory Tests ────────────────────────────────────────────────────

func TestGetScoreHistory(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-score-history"

	scores := []float64{0.5, 0.6, 0.55, 0.7, 0.75}
	for i, score := range scores {
		tracker.RecordIteration(runID, IterationRecord{
			Iteration: i + 1,
			Score:     score,
			Timestamp: time.Now(),
		})
	}

	history := GetScoreHistory(tracker, runID)
	if len(history) != len(scores) {
		t.Errorf("len(GetScoreHistory()) = %d, want %d", len(history), len(scores))
	}

	for i, score := range scores {
		if history[i] != score {
			t.Errorf("history[%d] = %v, want %v", i, history[i], score)
		}
	}
}

func TestGetScoreHistory_Empty(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	history := GetScoreHistory(tracker, "nonexistent")
	if len(history) != 0 {
		t.Errorf("len(GetScoreHistory()) = %d, want 0", len(history))
	}
}

// ─── GetProgressTrend Tests ───────────────────────────────────────────────────

func TestGetProgressTrend_Improving(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-improving"

	// Scores increasing over iterations
	scores := []float64{0.5, 0.55, 0.6, 0.65, 0.7}
	for i, score := range scores {
		tracker.RecordIteration(runID, IterationRecord{
			Iteration: i + 1,
			Score:     score,
			Timestamp: time.Now(),
		})
	}

	trend := GetProgressTrend(tracker, runID)
	if trend != "improving" {
		t.Errorf("GetProgressTrend() = %q, want %q", trend, "improving")
	}
}

func TestGetProgressTrend_Declining(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-declining"

	// Scores decreasing over iterations
	scores := []float64{0.8, 0.75, 0.7, 0.65, 0.6}
	for i, score := range scores {
		tracker.RecordIteration(runID, IterationRecord{
			Iteration: i + 1,
			Score:     score,
			Timestamp: time.Now(),
		})
	}

	trend := GetProgressTrend(tracker, runID)
	if trend != "declining" {
		t.Errorf("GetProgressTrend() = %q, want %q", trend, "declining")
	}
}

func TestGetProgressTrend_Stagnant(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-stagnant"

	// Scores staying flat
	scores := []float64{0.5, 0.51, 0.505, 0.502, 0.5}
	for i, score := range scores {
		tracker.RecordIteration(runID, IterationRecord{
			Iteration: i + 1,
			Score:     score,
			Timestamp: time.Now(),
		})
	}

	trend := GetProgressTrend(tracker, runID)
	if trend != "stagnant" {
		t.Errorf("GetProgressTrend() = %q, want %q", trend, "stagnant")
	}
}

func TestGetProgressTrend_Stagnant_SingleIteration(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	runID := "run-single"

	tracker.RecordIteration(runID, IterationRecord{
		Iteration: 1,
		Score:     0.5,
		Timestamp: time.Now(),
	})

	trend := GetProgressTrend(tracker, runID)
	if trend != "stagnant" {
		t.Errorf("GetProgressTrend() with 1 iteration = %q, want %q", trend, "stagnant")
	}
}

func TestGetProgressTrend_Stagnant_NoRecords(t *testing.T) {
	tracker := NewInMemoryIterationTracker()
	trend := GetProgressTrend(tracker, "nonexistent")
	if trend != "stagnant" {
		t.Errorf("GetProgressTrend() with no records = %q, want %q", trend, "stagnant")
	}
}

// ─── MockIterationTracker Tests ───────────────────────────────────────────────

func TestMockIterationTracker(t *testing.T) {
	tracker := &MockIterationTracker{
		Records: make([]IterationRecord, 0),
	}
	runID := "mock-run"

	rec1 := IterationRecord{Iteration: 1, Score: 0.5}
	tracker.RecordIteration(runID, rec1)

	recs := tracker.GetIterationRecords(runID)
	if len(recs) != 1 {
		t.Errorf("len(GetIterationRecords()) = %d, want 1", len(recs))
	}

	latest := tracker.GetLatestIteration(runID)
	if latest.Iteration != 1 {
		t.Errorf("GetLatestIteration().Iteration = %d, want 1", latest.Iteration)
	}

	tracker.ClearRunRecords(runID)
	if len(tracker.Records) != 0 {
		t.Errorf("after clear: len(Records) = %d, want 0", len(tracker.Records))
	}
}

func TestMockIterationTracker_CustomFunctions(t *testing.T) {
	tracker := &MockIterationTracker{
		Records: make([]IterationRecord, 0),
	}
	runID := "custom-run"

	// Set up custom functions
	tracker.GetRecordsFunc = func(rid string) []IterationRecord {
		return []IterationRecord{
			{Iteration: 99, Score: 0.99},
		}
	}

	recs := tracker.GetIterationRecords(runID)
	if len(recs) != 1 {
		t.Errorf("len(GetIterationRecords()) = %d, want 1", len(recs))
	}
	if recs[0].Iteration != 99 {
		t.Errorf("recs[0].Iteration = %d, want 99", recs[0].Iteration)
	}
}