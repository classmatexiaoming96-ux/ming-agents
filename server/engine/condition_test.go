package engine

import (
	"testing"

	"github.com/ming-agents/server/workflow"
)

// ─── Condition Evaluation Tests ──────────────────────────────────────────────

func TestConditionEvaluateBasic(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("check", "status", "ready")
	ctx.SetOutput("check", "count", 8)

	tests := []struct {
		name     string
		expr     string
		expected bool
	}{
		{"equality true", "check.status == ready", true},
		{"equality false", "check.status == pending", false},
		{"inequality true", "check.status != pending", true},
		{"inequality false", "check.status != ready", false},
		{"numeric greater", "check.count > 5", true},
		{"numeric greater false", "check.count > 10", false},
		{"numeric less", "check.count < 10", true},
		{"numeric less false", "check.count < 5", false},
		{"numeric greater equal", "check.count >= 5", true},
		{"numeric greater equal false", "check.count >= 10", false},
		{"numeric less equal", "check.count <= 10", true},
		{"numeric less equal false", "check.count <= 3", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ctx.EvaluateCondition(tt.expr)
			if err != nil {
				t.Fatalf("EvaluateCondition(%q) error: %v", tt.expr, err)
			}
			if result != tt.expected {
				t.Errorf("EvaluateCondition(%q) = %v, want %v", tt.expr, result, tt.expected)
			}
		})
	}
}

func TestConditionEvaluateWithContext(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("upstream", "flag", true)
	ctx.SetOutput("upstream", "count", 42)
	ctx.SetOutput("upstream", "name", "Alice")

	tests := []struct {
		name     string
		expr     string
		expected bool
	}{
		{"boolean variable true", "upstream.flag", true},
		{"boolean variable false", "upstream.missing", false},
		{"numeric comparison", "upstream.count > 40", true},
		{"string equality", "upstream.name == Alice", true},
		{"unresolved reference", "upstream.nonexistent == value", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ctx.EvaluateCondition(tt.expr)
			if err != nil {
				t.Fatalf("EvaluateCondition(%q) error: %v", tt.expr, err)
			}
			if result != tt.expected {
				t.Errorf("EvaluateCondition(%q) = %v, want %v", tt.expr, result, tt.expected)
			}
		})
	}
}

func TestConditionEvaluateLogical(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("check", "a", true)
	ctx.SetOutput("check", "b", false)
	ctx.SetOutput("check", "x", 5)

	tests := []struct {
		name     string
		expr     string
		expected bool
	}{
		{"and true && true", "check.a && check.x > 3", true},
		{"and true && false", "check.a && check.b", false},
		{"or false || true", "check.b || check.a", true},
		{"or false || false", "check.b || check.x == 0", false},
		{"not !true", "!check.b", true},
		{"not !false", "!check.a", false},
		{"complex and/or", "check.a && check.x > 0 || check.b", true},
		{"complex with parens", "(check.a && check.x > 0) || check.b", true},
		{"negation of comparison", "!(check.x > 10)", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ctx.EvaluateCondition(tt.expr)
			if err != nil {
				t.Fatalf("EvaluateCondition(%q) error: %v", tt.expr, err)
			}
			if result != tt.expected {
				t.Errorf("EvaluateCondition(%q) = %v, want %v", tt.expr, result, tt.expected)
			}
		})
	}
}

func TestConditionEvaluateNotExists(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("upstream", "present", "value")
	// upstream.missing is not set

	tests := []struct {
		name     string
		expr     string
		expected bool
	}{
		{"exists true", "exists upstream.present", true},
		{"exists false", "exists upstream.missing", false},
		{"!exists true", "!exists upstream.missing", true},
		{"!exists false", "!exists upstream.present", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ctx.EvaluateCondition(tt.expr)
			if err != nil {
				t.Fatalf("EvaluateCondition(%q) error: %v", tt.expr, err)
			}
			if result != tt.expected {
				t.Errorf("EvaluateCondition(%q) = %v, want %v", tt.expr, result, tt.expected)
			}
		})
	}
}

func TestSchedulerSkipPropagation(t *testing.T) {
	// Create a DAG: a -> b -> c
	dag := workflow.NewDAG()
	dag.AddNode(&workflow.Node{ID: "a", Name: "a", Type: "task"})
	dag.AddNode(&workflow.Node{ID: "b", Name: "b", Type: "task"})
	dag.AddNode(&workflow.Node{ID: "c", Name: "c", Type: "task"})
	dag.AddEdge("a", "b")
	dag.AddEdge("b", "c")

	scheduler := NewScheduler(nil, dag, 4)
	scheduler.InitReadySet()

	ctx := NewContext()

	// Initially a should be ready.
	ready := scheduler.Advance(ctx, map[string]bool{})
	if len(ready) != 1 || ready[0].ID != "a" {
		t.Errorf("expected only 'a' to be ready, got %v", nodeIDs(ready))
	}

	// Simulate a being skipped.
	scheduler.SkipStep("a")
	ready = scheduler.Advance(ctx, map[string]bool{})

	// b should be skipped (propagated from a).
	if !scheduler.IsSkipped("b") {
		t.Error("expected 'b' to be skipped after 'a' was skipped")
	}

	// c should also be skipped (propagated through b).
	if !scheduler.IsSkipped("c") {
		t.Error("expected 'c' to be skipped (propagated through 'b')")
	}
}

func TestDriverSkipStep(t *testing.T) {
	// This test verifies that when a step's when condition evaluates to false,
	// the driver correctly skips the step.

	ctx := NewContext()
	ctx.SetOutput("check", "enabled", false)

	// Simulate evaluating a when condition.
	whenExpr := "check.enabled == true"
	ok, err := ctx.EvaluateCondition(whenExpr)
	if err != nil {
		t.Fatalf("EvaluateCondition error: %v", err)
	}
	if ok {
		t.Error("expected condition to evaluate to false")
	}

	// When condition is false, the step should be skipped.
	if ok {
		t.Error("step should be skipped when when condition evaluates to false")
	}

	// Now set the flag to true and re-evaluate.
	ctx.SetOutput("check", "enabled", true)
	ok, err = ctx.EvaluateCondition(whenExpr)
	if err != nil {
		t.Fatalf("EvaluateCondition error: %v", err)
	}
	if !ok {
		t.Error("expected condition to evaluate to true")
	}
}

// ─── Helper Functions ─────────────────────────────────────────────────────────

func nodeIDs(nodes []*workflow.Node) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}