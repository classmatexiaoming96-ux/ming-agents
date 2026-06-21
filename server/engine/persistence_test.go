package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ming-agents/server/workflow"
)

// TestPersistState tests that the scheduler can persist its state to a file.
func TestPersistState(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	_ = dag.AddEdge("A", "B")

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	runID := uuid.New()

	// Mark A as completed with outputs.
	s.MarkStepCompleted("A", map[string]any{"result": "output_a"})

	// Persist.
	if err := s.PersistState(runID); err != nil {
		t.Fatalf("PersistState failed: %v", err)
	}

	// Verify checkpoint file exists.
	path := filepath.Join(checkpointDir, runID.String(), "checkpoint.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("checkpoint file not found: %v", err)
	}

	var ckpt SchedulerCheckpoint
	if err := json.Unmarshal(data, &ckpt); err != nil {
		t.Fatalf("parse checkpoint: %v", err)
	}

	if ckpt.Version != "1.0" {
		t.Errorf("expected version 1.0, got %s", ckpt.Version)
	}
	if !ckpt.CompletedSteps["A"] {
		t.Error("expected A in completed steps")
	}
	if ckpt.CompletedSteps["B"] {
		t.Error("B should not be completed yet")
	}
	if ckpt.StepOutputs["A"] == nil {
		t.Error("expected outputs for A")
	}
}

// TestRecoverState tests that the scheduler can recover its state from a checkpoint.
func TestRecoverState(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	_ = dag.AddEdge("A", "B")

	runID := uuid.New()

	// First, set up a scheduler and persist some state.
	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()
	s.MarkStepCompleted("A", map[string]any{"result": "output_a"})
	s.skipped["B"] = true // B was skipped due to when condition

	if err := s.PersistState(runID); err != nil {
		t.Fatalf("PersistState failed: %v", err)
	}

	// Create a fresh scheduler and recover.
	s2 := NewScheduler(nil, dag, 4)
	s2.InitReadySet()

	recovered, err := s2.RecoverState(runID)
	if err != nil {
		t.Fatalf("RecoverState failed: %v", err)
	}
	if !recovered {
		t.Fatal("expected recovered=true")
	}

	// Verify state was restored.
	if !s2.completedSteps["A"] {
		t.Error("expected A to be recovered as completed")
	}
	if !s2.skipped["B"] {
		t.Error("expected B to be recovered as skipped")
	}
	if s2.stepOutputs["A"] == nil {
		t.Error("expected outputs for A to be recovered")
	}
}

// TestRecoverStateNotFound tests that RecoverState returns false when no checkpoint exists.
func TestRecoverStateNotFound(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})

	s := NewScheduler(nil, dag, 4)
	recovered, err := s.RecoverState(uuid.New())
	if err != nil {
		t.Fatalf("RecoverState should not error for missing checkpoint: %v", err)
	}
	if recovered {
		t.Error("expected recovered=false for missing checkpoint")
	}
}

// TestRecoverWithPartialCompletion tests recovery when some steps are completed
// and others are still pending (simulating partial completion before crash).
func TestRecoverWithPartialCompletion(t *testing.T) {
	dag := workflow.NewDAG()
	// A → B → C (linear chain)
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	dag.AddNode(&workflow.Node{ID: "C", Name: "C"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("B", "C")

	runID := uuid.New()

	// Simulate a run where A completed and B is in progress.
	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()
	s.MarkStepCompleted("A", map[string]any{"status": "done", "value": 42})

	if err := s.PersistState(runID); err != nil {
		t.Fatalf("PersistState failed: %v", err)
	}

	// Recover into a fresh scheduler.
	// Note: We do NOT call InitReadySet after recovery because the readySet
	// was built during the original execution (Advance calls) before the crash.
	// RecoverState restores completedSteps/skipped/stepOutputs but not readySet
	// (which is rebuilt dynamically during execution via Advance).
	s2 := NewScheduler(nil, dag, 4)

	recovered, err := s2.RecoverState(runID)
	if err != nil {
		t.Fatalf("RecoverState failed: %v", err)
	}
	if !recovered {
		t.Fatal("expected recovered=true")
	}

	// Only A should be in completed steps.
	if !s2.completedSteps["A"] {
		t.Error("expected A to be recovered as completed")
	}
	if s2.completedSteps["B"] {
		t.Error("B should not be marked as completed (was in progress)")
	}
	if s2.completedSteps["C"] {
		t.Error("C should not be marked as completed")
	}

	// A's outputs should be recovered.
	outputs, ok := s2.GetStepOutputs("A")
	if !ok {
		t.Error("expected outputs for A")
	}
	// Use float64 comparison since JSON unmarshals numbers as float64.
	if v, ok := outputs["value"].(float64); !ok || v != 42 {
		t.Errorf("expected value=42, got %v", outputs["value"])
	}

	// Simulate driver calling Advance with recovered completed steps.
	// This is how the driver actually uses the scheduler after recovery.
	// Note: completedSteps in the call must match the recovered state.
	completedSteps := s2.GetCompletedSteps()
	newlyReady := s2.Advance(nil, completedSteps)

	// After processing A's completion, B should become newly ready.
	foundB := false
	for _, n := range newlyReady {
		if n.ID == "B" {
			foundB = true
			break
		}
	}
	if !foundB {
		t.Error("expected B to be newly ready after Advance with recovered completedSteps")
	}

	// A should NOT be in newlyReady since it was already completed.
	for _, n := range newlyReady {
		if n.ID == "A" {
			t.Error("A should not be in newlyReady (already completed)")
		}
	}
}

// TestRecoverWithSkips tests recovery when a step was skipped due to a when condition.
func TestRecoverWithSkips(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B", When: stringPtr("A.status == done")})
	dag.AddNode(&workflow.Node{ID: "C", Name: "C"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("A", "C")

	runID := uuid.New()

	// Simulate a run where A completed, B was skipped, C should still run.
	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()
	s.MarkStepCompleted("A", map[string]any{"status": "done"})
	s.SkipStep("B")

	if err := s.PersistState(runID); err != nil {
		t.Fatalf("PersistState failed: %v", err)
	}

	// Recover.
	s2 := NewScheduler(nil, dag, 4)
	s2.InitReadySet()

	recovered, err := s2.RecoverState(runID)
	if err != nil {
		t.Fatalf("RecoverState failed: %v", err)
	}
	if !recovered {
		t.Fatal("expected recovered=true")
	}

	// A should be completed, B should be skipped.
	if !s2.completedSteps["A"] {
		t.Error("expected A to be recovered as completed")
	}
	if !s2.skipped["B"] {
		t.Error("expected B to be recovered as skipped")
	}

	// B should not be in ready set (was skipped).
	if s2.readySet["B"] != nil {
		t.Error("B should not be in ready set (was skipped)")
	}
}

// TestMarkStepCompleted tests that MarkStepCompleted correctly records completion and outputs.
func TestMarkStepCompleted(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	outputs := map[string]any{"result": "test_output", "count": 3}
	s.MarkStepCompleted("A", outputs)

	if !s.completedSteps["A"] {
		t.Error("expected A to be marked as completed")
	}

	gotOutputs, ok := s.GetStepOutputs("A")
	if !ok {
		t.Fatal("expected outputs for A")
	}
	if gotOutputs["result"] != "test_output" {
		t.Errorf("expected result=test_output, got %v", gotOutputs["result"])
	}
	if gotOutputs["count"] != 3 {
		t.Errorf("expected count=3, got %v", gotOutputs["count"])
	}
}

// TestGetCompletedSteps tests the getter for completed steps.
func TestGetCompletedSteps(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	s.MarkStepCompleted("A", nil)
	s.MarkStepCompleted("B", nil)

	completed := s.GetCompletedSteps()
	if len(completed) != 2 {
		t.Errorf("expected 2 completed steps, got %d", len(completed))
	}
	if !completed["A"] || !completed["B"] {
		t.Error("expected both A and B to be in completed set")
	}
}

// TestCheckpointFileLocation tests that the checkpoint is written to the correct location.
func TestCheckpointFileLocation(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})

	runID := uuid.New()

	s := NewScheduler(nil, dag, 4)
	s.MarkStepCompleted("A", nil)

	if err := s.PersistState(runID); err != nil {
		t.Fatalf("PersistState failed: %v", err)
	}

	expectedPath := filepath.Join(checkpointDir, runID.String(), "checkpoint.json")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("checkpoint file not at expected location %s: %v", expectedPath, err)
	}
}

// Helper for when we need a *string.
func stringPtr(s string) *string {
	return &s
}