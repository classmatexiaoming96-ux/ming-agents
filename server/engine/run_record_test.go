package engine

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRunRecordStore_SaveAndGet(t *testing.T) {
	store := NewInMemoryRunRecordStore()
	runID := uuid.New()

	rec := RunRecord{
		RunID:          runID,
		TemplateName:   "bugfix",
		Timestamp:      time.Now().UTC(),
		ResolvedParams: map[string]map[string]any{},
		EffectiveThresholds: map[string]float64{},
		SkippedSteps:   []SkippedStep{},
		RunStatus:      "completed",
		TotalSteps:     3,
	}
	rec.ResolvedParams["step1"] = map[string]any{"prompt": "fix the bug"}

	if err := store.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord failed: %v", err)
	}

	got, err := store.GetRecord(runID)
	if err != nil {
		t.Fatalf("GetRecord failed: %v", err)
	}
	if got.RunID != runID {
		t.Errorf("RunID mismatch: got %v, want %v", got.RunID, runID)
	}
	if got.TemplateName != "bugfix" {
		t.Errorf("TemplateName mismatch: got %v, want bugfix", got.TemplateName)
	}
	if got.TotalSteps != 3 {
		t.Errorf("TotalSteps mismatch: got %v, want 3", got.TotalSteps)
	}
	if got.ResolvedParams["step1"] == nil {
		t.Error("ResolvedParams[step1] should not be nil")
	}
}

func TestRunRecordStore_GetNotFound(t *testing.T) {
	store := NewInMemoryRunRecordStore()
	runID := uuid.New()

	_, err := store.GetRecord(runID)
	if err == nil {
		t.Error("GetRecord should return error for non-existent record")
	}
	_, ok := err.(*RunRecordNotFoundError)
	if !ok {
		t.Errorf("error should be RunRecordNotFoundError, got %T", err)
	}
}

func TestRunRecordStore_ListByTemplate(t *testing.T) {
	store := NewInMemoryRunRecordStore()

	// Create records for different templates.
	for i := 0; i < 3; i++ {
		rec := RunRecord{
			RunID:          uuid.New(),
			TemplateName:   "bugfix",
			Timestamp:      time.Now().UTC(),
			ResolvedParams: map[string]map[string]any{},
			EffectiveThresholds: map[string]float64{},
			SkippedSteps:   []SkippedStep{},
			RunStatus:      "completed",
		}
		_ = store.SaveRecord(rec)
	}
	// Add one for a different template.
	rec := RunRecord{
		RunID:          uuid.New(),
		TemplateName:   "review",
		Timestamp:      time.Now().UTC(),
		ResolvedParams: map[string]map[string]any{},
		EffectiveThresholds: map[string]float64{},
		SkippedSteps:   []SkippedStep{},
		RunStatus:      "completed",
	}
	_ = store.SaveRecord(rec)

	bugfixRecs, err := store.ListRecordsByTemplate("bugfix")
	if err != nil {
		t.Fatalf("ListRecordsByTemplate failed: %v", err)
	}
	if len(bugfixRecs) != 3 {
		t.Errorf("bugfix records: got %d, want 3", len(bugfixRecs))
	}

	reviewRecs, err := store.ListRecordsByTemplate("review")
	if err != nil {
		t.Fatalf("ListRecordsByTemplate failed: %v", err)
	}
	if len(reviewRecs) != 1 {
		t.Errorf("review records: got %d, want 1", len(reviewRecs))
	}

	emptyRecs, err := store.ListRecordsByTemplate("nonexistent")
	if err != nil {
		t.Fatalf("ListRecordsByTemplate failed: %v", err)
	}
	if len(emptyRecs) != 0 {
		t.Errorf("nonexistent records: got %d, want 0", len(emptyRecs))
	}
}

func TestRunRecordStore_ReplayRun(t *testing.T) {
	store := NewInMemoryRunRecordStore()
	runID := uuid.New()

	rec := RunRecord{
		RunID:          runID,
		TemplateName:   "bugfix",
		Timestamp:      time.Now().UTC(),
		ResolvedParams: map[string]map[string]any{
			"fix": {"prompt": "fix ${bug.title}", "model": "claude"},
		},
		EffectiveThresholds: map[string]float64{"score": 0.8},
		SkippedSteps:   []SkippedStep{},
		RunStatus:      "completed",
	}
	_ = store.SaveRecord(rec)

	got, err := store.ReplayRun(runID)
	if err != nil {
		t.Fatalf("ReplayRun failed: %v", err)
	}
	if got.RunID != rec.RunID {
		t.Errorf("RunID mismatch: got %v, want %v", got.RunID, rec.RunID)
	}
	if got.ResolvedParams["fix"] == nil {
		t.Error("ResolvedParams[fix] should not be nil")
	}
}

func TestReplayParams(t *testing.T) {
	rec := RunRecord{
		RunID:          uuid.New(),
		TemplateName:   "bugfix",
		Timestamp:      time.Now().UTC(),
		ResolvedParams: map[string]map[string]any{
			"fix":   {"prompt": "fix ${bug.title}", "model": "claude"},
			"test":  {"assertions": []string{"no_error", "output_exists"}},
		},
		EffectiveThresholds: map[string]float64{
			"score":      0.8,
			"confidence": 0.95,
		},
		SkippedSteps: []SkippedStep{
			{StepName: "cleanup", Reason: "when expression \"skip_cleanup == true\" evaluated to false"},
		},
		RunStatus: "completed",
	}

	params := ReplayParams(rec)
	if params["fix"] == nil {
		t.Error("params[fix] should not be nil")
	}
	if params["fix"]["prompt"] != "fix ${bug.title}" {
		t.Errorf("params[fix][prompt] = %v, want fix ${bug.title}", params["fix"]["prompt"])
	}
	if params["test"] == nil {
		t.Error("params[test] should not be nil")
	}

	thresholds := ReplayThresholds(rec)
	if thresholds["score"] != 0.8 {
		t.Errorf("thresholds[score] = %v, want 0.8", thresholds["score"])
	}
	if thresholds["confidence"] != 0.95 {
		t.Errorf("thresholds[confidence] = %v, want 0.95", thresholds["confidence"])
	}

	skipped := ReplaySkippedSteps(rec)
	if len(skipped) != 1 {
		t.Errorf("skipped steps: got %d, want 1", len(skipped))
	}
	if skipped[0].StepName != "cleanup" {
		t.Errorf("skipped[0].StepName = %v, want cleanup", skipped[0].StepName)
	}
}

func TestRunRecorder_RecordAndSave(t *testing.T) {
	store := NewInMemoryRunRecordStore()
	degradationStore := NewInMemoryDegradationStore()
	runID := uuid.New()

	rec := NewRunRecorder(store, runID, "bugfix", 5, degradationStore)

	// Record resolved params.
	rec.RecordResolvedParams("fix", map[string]any{
		"prompt": "fix ${bug.title}",
		"model":  "claude",
	})
	rec.RecordResolvedParams("test", map[string]any{
		"assertions": []string{"no_error"},
	})

	// Record an assertion.
	rec.RecordAssertion("test", "output_exists", true, "file.txt", nil)

	// Record a threshold.
	rec.RecordThreshold("score", 0.8)

	// Record a skipped step.
	rec.RecordSkippedStep("cleanup", "when condition false")

	// Save the record.
	if err := rec.Save("completed", nil); err != nil {
		t.Fatalf("rec.Save failed: %v", err)
	}

	// Verify by retrieving.
	got, err := store.GetRecord(runID)
	if err != nil {
		t.Fatalf("GetRecord failed: %v", err)
	}
	if got.TemplateName != "bugfix" {
		t.Errorf("TemplateName: got %v, want bugfix", got.TemplateName)
	}
	if got.TotalSteps != 5 {
		t.Errorf("TotalSteps: got %v, want 5", got.TotalSteps)
	}
	if got.ResolvedParams["fix"] == nil {
		t.Error("ResolvedParams[fix] should not be nil")
	}
	if len(got.EvaluatedAssertions) != 1 {
		t.Errorf("EvaluatedAssertions len: got %d, want 1", len(got.EvaluatedAssertions))
	}
	if got.EffectiveThresholds["score"] != 0.8 {
		t.Errorf("EffectiveThresholds[score]: got %v, want 0.8", got.EffectiveThresholds["score"])
	}
	if len(got.SkippedSteps) != 1 {
		t.Errorf("SkippedSteps len: got %d, want 1", len(got.SkippedSteps))
	}
}

func TestScheduler_ReplayParams(t *testing.T) {
	store := NewInMemoryRunRecordStore()
	runID := uuid.New()

	// Create and save a record.
	rec := RunRecord{
		RunID:          runID,
		TemplateName:   "bugfix",
		Timestamp:      time.Now().UTC(),
		ResolvedParams: map[string]map[string]any{
			"step1": {"prompt": "fix bug", "model": "claude"},
			"step2": {"prompt": "test fix"},
		},
		EffectiveThresholds: map[string]float64{},
		SkippedSteps:   []SkippedStep{},
		RunStatus:      "completed",
	}
	_ = store.SaveRecord(rec)

	// Create a scheduler.
	s := &Scheduler{
		stepOutputs: make(map[string]map[string]any),
	}

	// Replay params from the record.
	s.ReplayParams(rec.ResolvedParams)

	// Verify params were restored.
	if s.stepOutputs["step1"] == nil {
		t.Error("stepOutputs[step1] should not be nil after replay")
	}
	if s.stepOutputs["step1"]["prompt"] != "fix bug" {
		t.Errorf("stepOutputs[step1][prompt] = %v, want fix bug", s.stepOutputs["step1"]["prompt"])
	}
	if s.stepOutputs["step2"] == nil {
		t.Error("stepOutputs[step2] should not be nil after replay")
	}
}

func TestRunRecorder_RecordDegradation(t *testing.T) {
	store := NewInMemoryRunRecordStore()
	degradationStore := NewInMemoryDegradationStore()
	runID := uuid.New()

	rec := NewRunRecorder(store, runID, "bugfix", 5, degradationStore)

	// Record degradation events.
	_ = rec.RecordDegradation(DegradationEvent{
		Reason:         "model_unavailable",
		TriggeredParam: "model",
		OriginalValue:  "claude-3-opus",
		FallbackValue:  "claude-3-sonnet",
		Severity:       SeverityWarning,
		Message:        "Requested model unavailable, fell back to alternative",
		StepName:       "fix",
	})

	_ = rec.RecordDegradation(DegradationEvent{
		Reason:         "rate_limit",
		TriggeredParam: "model",
		OriginalValue:  "claude-3-sonnet",
		FallbackValue:  "claude-3-haiku",
		Severity:       SeverityError,
		Message:        "Rate limit hit, degraded to minimum tier",
		StepName:       "fix",
	})

	// Get the degradation alert.
	alert := rec.GetDegradationAlert()
	if alert == nil {
		t.Fatal("GetDegradationAlert should not return nil")
	}
	if alert.DegradationCount != 2 {
		t.Errorf("DegradationCount: got %d, want 2", alert.DegradationCount)
	}
	if alert.MostSevere != SeverityError {
		t.Errorf("MostSevere: got %v, want error", alert.MostSevere)
	}
	if alert.RunID != runID {
		t.Errorf("RunID: got %v, want %v", alert.RunID, runID)
	}

	// Save and verify alert is included in RunRecord.
	if err := rec.Save("completed", nil); err != nil {
		t.Fatalf("rec.Save failed: %v", err)
	}

	got, err := store.GetRecord(runID)
	if err != nil {
		t.Fatalf("GetRecord failed: %v", err)
	}
	if got.DegradationAlert == nil {
		t.Fatal("DegradationAlert should be included in RunRecord")
	}
	if got.DegradationAlert.DegradationCount != 2 {
		t.Errorf("DegradationAlert.DegradationCount: got %d, want 2", got.DegradationAlert.DegradationCount)
	}
	if got.DegradationAlert.MostSevere != SeverityError {
		t.Errorf("DegradationAlert.MostSevere: got %v, want error", got.DegradationAlert.MostSevere)
	}
}

func TestRunRecorder_RecordDegradation_NoStore(t *testing.T) {
	store := NewInMemoryRunRecordStore()
	// Create recorder WITHOUT degradation store.
	runID := uuid.New()
	rec := NewRunRecorder(store, runID, "bugfix", 5, nil)

	// RecordDegradation should not error even without a store.
	err := rec.RecordDegradation(DegradationEvent{
		Reason:         "model_unavailable",
		TriggeredParam: "model",
		Severity:       SeverityWarning,
		Message:        "Test",
	})
	if err != nil {
		t.Fatalf("RecordDegradation should not error without store: %v", err)
	}

	// GetDegradationAlert should return nil.
	alert := rec.GetDegradationAlert()
	if alert != nil {
		t.Error("GetDegradationAlert should return nil without store")
	}

	// Save should work without degradation alert.
	if err := rec.Save("completed", nil); err != nil {
		t.Fatalf("rec.Save failed: %v", err)
	}

	got, err := store.GetRecord(runID)
	if err != nil {
		t.Fatalf("GetRecord failed: %v", err)
	}
	if got.DegradationAlert != nil {
		t.Error("DegradationAlert should be nil when no degradation store configured")
	}
}