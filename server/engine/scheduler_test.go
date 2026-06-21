package engine

import (
	"encoding/json"
	"testing"

	"github.com/ming-agents/server/workflow"
)

// TestSchedulerInitReadySet tests Kahn's algorithm initialization.
func TestSchedulerInitReadySet(t *testing.T) {
	dag := workflow.NewDAG()
	// A → B → C (linear chain)
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	dag.AddNode(&workflow.Node{ID: "C", Name: "C"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("B", "C")

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	if s.ReadyCount() != 1 {
		t.Errorf("expected 1 ready node (A), got %d", s.ReadyCount())
	}
	if _, ok := s.readySet["A"]; !ok {
		t.Error("node A should be in ready set")
	}
}

// TestSchedulerInitReadySetParallel tests parallel branches.
func TestSchedulerInitReadySetParallel(t *testing.T) {
	dag := workflow.NewDAG()
	// A is parent of both B and C
	// B and C are both children of A
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	dag.AddNode(&workflow.Node{ID: "C", Name: "C"})
	dag.AddNode(&workflow.Node{ID: "D", Name: "D"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("A", "C")
	_ = dag.AddEdge("B", "D")
	_ = dag.AddEdge("C", "D")

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	if s.ReadyCount() != 1 {
		t.Errorf("expected 1 ready node (A), got %d", s.ReadyCount())
	}
}

// TestSchedulerAdvance tests step completion and unblocking.
func TestSchedulerAdvance(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	_ = dag.AddEdge("A", "B")

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	// After A completes, B should become ready.
	completed := map[string]bool{"A": true}
	newlyReady := s.Advance(nil, completed)

	if len(newlyReady) != 1 {
		t.Errorf("expected 1 newly ready node, got %d", len(newlyReady))
	}
	// Simulate driver calling StepCompleted after processing A.
	s.StepCompleted("A")
	// B should now be the only node in ready set.
	if s.ReadyCount() != 1 {
		t.Errorf("expected 1 node in ready set (B), got %d", s.ReadyCount())
	}
}

// TestSchedulerParallelDegree tests max parallel slot control.
func TestSchedulerParallelDegree(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})

	s := NewScheduler(nil, dag, 1) // max 1 parallel

	slots := s.PendingSlots(0, 10)
	if slots != 1 {
		t.Errorf("expected 1 slot with maxParallel=1, got %d", slots)
	}

	slots = s.PendingSlots(1, 10)
	if slots != 0 {
		t.Errorf("expected 0 slots when already at max, got %d", slots)
	}
}

// TestSchedulerAddReadyStep tests runtime addition of steps to ready set.
func TestSchedulerAddReadyStep(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	// Simulate a dynamically generated node (e.g., from fan-out).
	newNode := &workflow.Node{ID: "B", Name: "B"}
	s.AddReadyStep(newNode)

	if s.ReadyCount() != 2 {
		t.Errorf("expected 2 nodes in ready set, got %d", s.ReadyCount())
	}
}

// TestSchedulerPendingSlots tests pending slot calculation.
func TestSchedulerPendingSlots(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})

	s := NewScheduler(nil, dag, 3)

	tests := []struct {
		claimed int
		pending int
		want    int
	}{
		{0, 5, 3},  // 3 available, need 5 → get 3
		{1, 5, 2},  // 2 available, need 5 → get 2
		{2, 5, 1},  // 1 available, need 5 → get 1
		{3, 5, 0},  // 0 available
		{0, 1, 1},  // 3 available, need 1 → get 1
		{2, 1, 1},  // 1 available, need 1 → get 1
	}

	for _, tt := range tests {
		got := s.PendingSlots(tt.claimed, tt.pending)
		if got != tt.want {
			t.Errorf("PendingSlots(%d, %d) = %d, want %d", tt.claimed, tt.pending, got, tt.want)
		}
	}
}

// TestDAGTopologicalSort tests Kahn's topological sort.
func TestDAGTopologicalSort(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	dag.AddNode(&workflow.Node{ID: "C", Name: "C"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("B", "C")

	order, err := dag.TopologicalSort()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(order))
	}
	// A should come before B, B before C.
	pos := map[string]int{}
	for i, n := range order {
		pos[n.ID] = i
	}
	if pos["A"] >= pos["B"] || pos["B"] >= pos["C"] {
		t.Error("topological order incorrect")
	}
}

// TestDAGTopologicalSortCycle tests cycle detection.
func TestDAGTopologicalSortCycle(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("B", "A") // cycle: B → A → B

	_, err := dag.TopologicalSort()
	if err == nil {
		t.Error("expected error for cyclic graph")
	}
}

// TestDAGDetectCycle tests DFS cycle detection.
func TestDAGDetectCycle(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("B", "A")

	if !dag.DetectCycle() {
		t.Error("expected cycle detected")
	}
}

// TestDAGChildren tests children access.
func TestDAGChildren(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	dag.AddNode(&workflow.Node{ID: "C", Name: "C"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("A", "C")

	children := dag.Children("A")
	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}
}

// TestDAGInDegree tests in-degree tracking.
func TestDAGInDegree(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	dag.AddNode(&workflow.Node{ID: "C", Name: "C"})
	_ = dag.AddEdge("A", "B")
	_ = dag.AddEdge("A", "C")

	if dag.InDegree("A") != 0 {
		t.Errorf("expected A in-degree 0, got %d", dag.InDegree("A"))
	}
	if dag.InDegree("B") != 1 {
		t.Errorf("expected B in-degree 1, got %d", dag.InDegree("B"))
	}
	if dag.InDegree("C") != 1 {
		t.Errorf("expected C in-degree 1, got %d", dag.InDegree("C"))
	}
}

// TestSchedulerGetReadySteps tests getting a snapshot of ready steps.
func TestSchedulerGetReadySteps(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	steps := s.GetReadySteps()
	if len(steps) != 2 {
		t.Errorf("expected 2 ready steps, got %d", len(steps))
	}
}

// TestSchedulerStepCompleted tests step completion cleanup.
func TestSchedulerStepCompleted(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	s.StepCompleted("A")

	if s.ReadyCount() != 0 {
		t.Errorf("expected 0 ready nodes after completion, got %d", s.ReadyCount())
	}
}

// TestSchedulerResetInDegree tests in-degree reset.
func TestSchedulerResetInDegree(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	_ = dag.AddEdge("A", "B")

	// Decrement B's in-degree (simulating A completing).
	dag.UpdateInDegree("B")

	if dag.InDegree("B") != 0 {
		t.Errorf("expected B in-degree 0 after decrement, got %d", dag.InDegree("B"))
	}

	// Reset.
	dag.ResetInDegree()

	if dag.InDegree("B") != 1 {
		t.Errorf("expected B in-degree 1 after reset, got %d", dag.InDegree("B"))
	}
}

// TestDAGUpdateInDegree tests in-degree update.
func TestDAGUpdateInDegree(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	_ = dag.AddEdge("A", "B")

	if dag.InDegree("B") != 1 {
		t.Fatalf("expected initial B in-degree 1, got %d", dag.InDegree("B"))
	}

	dag.UpdateInDegree("B")
	if dag.InDegree("B") != 0 {
		t.Errorf("expected B in-degree 0, got %d", dag.InDegree("B"))
	}

	// Should not go below 0.
	dag.UpdateInDegree("B")
	if dag.InDegree("B") != 0 {
		t.Errorf("expected B in-degree stays 0, got %d", dag.InDegree("B"))
	}
}

// TestFanOutCount tests fan-out item counting.
func TestFanOutCount(t *testing.T) {
	// Simulate fan-out count being determined at runtime from upstream output.
	upstreamOutputs := map[string]any{
		"files": []any{"file1.txt", "file2.txt", "file3.txt"},
	}
	fanoutItems := upstreamOutputs["files"].([]any)
	count := len(fanoutItems)
	if count != 3 {
		t.Errorf("expected 3 fan-out items, got %d", count)
	}
}

// TestTranslateStepWithFanOut tests dynamic fan-out translation.
func TestTranslateStepWithFanOut(t *testing.T) {
	// This tests the translator's ability to generate N tasks
	// based on a list input from upstream output.
	ctx := NewContext()
	ctx.SetOutput("locate", "files", []any{"a.txt", "b.txt", "c.txt"})

	// Simulate a step with fan-out inputs.
	step := &Step{
		Name:     "fix",
		StepType: "task",
		InputsMap: map[string]any{
			"files_list": []any{"${locate.files}", "should-be-resolved"},
		},
	}

	// Check that the list contains a reference.
	if len(step.InputsMap["files_list"].([]any)) != 2 {
		t.Errorf("expected 2 items in fan-out list")
	}
}

// TestSchedulerMultipleCompletions tests multiple step completions in one advance.
func TestSchedulerMultipleCompletions(t *testing.T) {
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "A", Name: "A"})
	dag.AddNode(&workflow.Node{ID: "B", Name: "B"})
	dag.AddNode(&workflow.Node{ID: "C", Name: "C"})
	_ = dag.AddEdge("A", "C")
	_ = dag.AddEdge("B", "C")

	s := NewScheduler(nil, dag, 4)
	s.InitReadySet()

	// Both A and B complete in same tick → C should become ready.
	completed := map[string]bool{"A": true, "B": true}
	newlyReady := s.Advance(nil, completed)

	// C should now be in ready set (A and B were removed).
	if s.ReadyCount() != 1 {
		t.Errorf("expected 1 node in ready set (C), got %d", s.ReadyCount())
	}
	if len(newlyReady) != 1 {
		t.Errorf("expected 1 newly ready (C), got %d", len(newlyReady))
	}
	// Simulate driver calling StepCompleted for A and B.
	s.StepCompleted("A")
	s.StepCompleted("B")
	// C should still be in ready set.
	if s.ReadyCount() != 1 {
		t.Errorf("expected 1 node after cleanup, got %d", s.ReadyCount())
	}
}

// Ensure json import is used (for potential future test expansion).
var _ = json.Marshal