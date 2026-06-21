package engine

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDegradationStore_SaveAndGet(t *testing.T) {
	store := NewInMemoryDegradationStore()
	runID := uuid.New()

	evt := DegradationEvent{
		EventID:        uuid.New(),
		RunID:          runID,
		Timestamp:      time.Now().UTC(),
		Reason:         "model_unavailable",
		TriggeredParam: "model",
		OriginalValue:  "claude-3-opus",
		FallbackValue:  "claude-3-sonnet",
		Severity:       SeverityWarning,
		Message:        "Requested model unavailable, fell back to alternative",
		StepName:       "fix",
	}

	if err := store.SaveDegradation(evt); err != nil {
		t.Fatalf("SaveDegradation failed: %v", err)
	}

	events, err := store.GetDegradationsByRun(runID)
	if err != nil {
		t.Fatalf("GetDegradationsByRun failed: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("events count: got %d, want 1", len(events))
	}
	if events[0].Reason != "model_unavailable" {
		t.Errorf("Reason: got %v, want model_unavailable", events[0].Reason)
	}
	if events[0].OriginalValue != "claude-3-opus" {
		t.Errorf("OriginalValue: got %v, want claude-3-opus", events[0].OriginalValue)
	}
	if events[0].FallbackValue != "claude-3-sonnet" {
		t.Errorf("FallbackValue: got %v, want claude-3-sonnet", events[0].FallbackValue)
	}
}

func TestDegradationStore_GetEmpty(t *testing.T) {
	store := NewInMemoryDegradationStore()
	runID := uuid.New()

	events, err := store.GetDegradationsByRun(runID)
	if err != nil {
		t.Fatalf("GetDegradationsByRun failed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events count: got %d, want 0", len(events))
	}
}

func TestDegradationStore_GetDegradationCount(t *testing.T) {
	store := NewInMemoryDegradationStore()
	runID := uuid.New()

	// Add 3 events.
	for i := 0; i < 3; i++ {
		evt := DegradationEvent{
			EventID:        uuid.New(),
			RunID:          runID,
			Timestamp:      time.Now().UTC(),
			Reason:         "rate_limit",
			TriggeredParam: "model",
			Severity:       SeverityInfo,
			Message:        "Rate limit hit",
		}
		_ = store.SaveDegradation(evt)
	}

	count := store.GetDegradationCount(runID)
	if count != 3 {
		t.Errorf("count: got %d, want 3", count)
	}

	// Count for non-existent run should be 0.
	otherRun := uuid.New()
	otherCount := store.GetDegradationCount(otherRun)
	if otherCount != 0 {
		t.Errorf("otherCount: got %d, want 0", otherCount)
	}
}

func TestDegradationStore_GetDegradationAlert(t *testing.T) {
	store := NewInMemoryDegradationStore()
	runID := uuid.New()

	// Add an info event.
	store.SaveDegradation(DegradationEvent{
		EventID:        uuid.New(),
		RunID:          runID,
		Timestamp:      time.Now().UTC(),
		Reason:         "timeout",
		TriggeredParam: "timeout_ms",
		OriginalValue:  5000,
		FallbackValue:  10000,
		Severity:       SeverityInfo,
		Message:        "Timeout increased",
	})

	// Add a warning event.
	store.SaveDegradation(DegradationEvent{
		EventID:        uuid.New(),
		RunID:          runID,
		Timestamp:      time.Now().UTC(),
		Reason:         "rate_limit",
		TriggeredParam: "model",
		OriginalValue:  "claude-3-opus",
		FallbackValue:  "claude-3-sonnet",
		Severity:       SeverityWarning,
		Message:        "Rate limit hit",
	})

	// Add an error event.
	store.SaveDegradation(DegradationEvent{
		EventID:        uuid.New(),
		RunID:          runID,
		Timestamp:      time.Now().UTC(),
		Reason:         "model_unavailable",
		TriggeredParam: "model",
		OriginalValue:  "claude-3-opus",
		FallbackValue:  "claude-3-haiku",
		Severity:       SeverityError,
		Message:        "Model unavailable, degraded to minimum tier",
	})

	alert, err := store.GetDegradationAlert(runID)
	if err != nil {
		t.Fatalf("GetDegradationAlert failed: %v", err)
	}
	if alert.DegradationCount != 3 {
		t.Errorf("DegradationCount: got %d, want 3", alert.DegradationCount)
	}
	if alert.MostSevere != SeverityError {
		t.Errorf("MostSevere: got %v, want error", alert.MostSevere)
	}
	if alert.RunID != runID {
		t.Errorf("RunID: got %v, want %v", alert.RunID, runID)
	}
	if len(alert.DegradationList) != 3 {
		t.Errorf("DegradationList len: got %d, want 3", len(alert.DegradationList))
	}
}

func TestSeverityOrder(t *testing.T) {
	tests := []struct {
		severity DegradationSeverity
		want     int
	}{
		{SeverityInfo, 1},
		{SeverityWarning, 2},
		{SeverityError, 3},
	}

	for _, tc := range tests {
		got := SeverityOrder(tc.severity)
		if got != tc.want {
			t.Errorf("SeverityOrder(%v): got %d, want %d", tc.severity, got, tc.want)
		}
	}
}

func TestMostSevereSeverity(t *testing.T) {
	tests := []struct {
		a, b   DegradationSeverity
		expect DegradationSeverity
	}{
		{SeverityInfo, SeverityInfo, SeverityInfo},
		{SeverityInfo, SeverityWarning, SeverityWarning},
		{SeverityInfo, SeverityError, SeverityError},
		{SeverityWarning, SeverityInfo, SeverityWarning},
		{SeverityWarning, SeverityWarning, SeverityWarning},
		{SeverityWarning, SeverityError, SeverityError},
		{SeverityError, SeverityInfo, SeverityError},
		{SeverityError, SeverityWarning, SeverityError},
		{SeverityError, SeverityError, SeverityError},
	}

	for _, tc := range tests {
		got := MostSevereSeverity(tc.a, tc.b)
		if got != tc.expect {
			t.Errorf("MostSevereSeverity(%v, %v): got %v, want %v", tc.a, tc.b, got, tc.expect)
		}
	}
}

func TestDegradationEvent_Immutability(t *testing.T) {
	store := NewInMemoryDegradationStore()
	runID := uuid.New()

	evt := DegradationEvent{
		EventID:        uuid.New(),
		RunID:          runID,
		Timestamp:      time.Now().UTC(),
		Reason:         "model_unavailable",
		TriggeredParam: "model",
		Severity:       SeverityWarning,
		Message:        "Test",
	}
	_ = store.SaveDegradation(evt)

	// Get events and modify the returned slice.
	events, _ := store.GetDegradationsByRun(runID)
	events[0].Message = "MODIFIED"

	// Get again and verify original is unchanged.
	events2, _ := store.GetDegradationsByRun(runID)
	if events2[0].Message == "MODIFIED" {
		t.Error("External mutation should not affect stored events")
	}
}