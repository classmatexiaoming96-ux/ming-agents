package workflow

import (
	"encoding/json"
	"testing"
)

func TestDefaultWorkflowSpecRunsReviewBeforeEvaluation(t *testing.T) {
	spec := DefaultWorkflowSpec

	want := []struct {
		id      string
		kind    NodeKind
		depends []string
	}{
		{id: "clarification", kind: NodeKindClarification},
		{id: "planning", kind: NodeKindPlanning, depends: []string{"clarification"}},
		{id: "development", kind: NodeKindDevelopment, depends: []string{"planning"}},
		{id: "review", kind: NodeKindReview, depends: []string{"development"}},
		{id: "evaluation", kind: NodeKindEvaluation, depends: []string{"review"}},
	}

	if len(spec.Nodes) != len(want) {
		t.Fatalf("DefaultWorkflowSpec nodes = %d, want %d", len(spec.Nodes), len(want))
	}
	for i, expected := range want {
		got := spec.Nodes[i]
		if got.ID != expected.id || got.Kind != expected.kind {
			t.Fatalf("node %d = (%q, %q), want (%q, %q)", i, got.ID, got.Kind, expected.id, expected.kind)
		}
		if !sameStrings(got.DependsOn, expected.depends) {
			t.Fatalf("node %q dependencies = %v, want %v", got.ID, got.DependsOn, expected.depends)
		}
	}
}

func TestDefaultWorkflowSpecHasNoEvaluationToReviewDependency(t *testing.T) {
	for _, node := range DefaultWorkflowSpec.Nodes {
		if node.ID != "review" {
			continue
		}
		for _, dep := range node.DependsOn {
			if dep == "evaluation" {
				t.Fatal("review must not depend on evaluation in the default workflow")
			}
		}
		return
	}
	t.Fatal("default workflow is missing review node")
}

func TestDefaultWorkflowSpecRetryAndRollbackPolicy(t *testing.T) {
	want := map[string]struct {
		maxRetries         int
		retryOn            []FailureClass
		rollbackMax        int
		rollbackScope      string
		rollbackOnContract RollbackAction
	}{
		"clarification": {
			maxRetries:    1,
			retryOn:       []FailureClass{FailureClassTransient, FailureClassMissingEvidence, FailureClassInconclusive},
			rollbackMax:   3,
			rollbackScope: "clarification",
		},
		"planning": {
			maxRetries:         2,
			retryOn:            []FailureClass{FailureClassTransient, FailureClassContractError, FailureClassMissingEvidence, FailureClassInconclusive},
			rollbackMax:        3,
			rollbackScope:      "planning",
			rollbackOnContract: RollbackActionRegeneratePlan,
		},
		"development": {
			maxRetries:         1,
			retryOn:            []FailureClass{FailureClassTransient, FailureClassValidatorIssue, FailureClassMissingEvidence},
			rollbackMax:        3,
			rollbackScope:      "development",
			rollbackOnContract: RollbackActionRegenerateSubtask,
		},
		"review": {
			maxRetries:         1,
			retryOn:            []FailureClass{FailureClassTransient, FailureClassContractError, FailureClassMissingEvidence, FailureClassInconclusive},
			rollbackMax:        1,
			rollbackScope:      "review",
			rollbackOnContract: RollbackActionRetryReport,
		},
		"evaluation": {
			maxRetries:    2,
			retryOn:       []FailureClass{FailureClassTransient, FailureClassValidatorIssue, FailureClassMissingEvidence, FailureClassInconclusive},
			rollbackMax:   2,
			rollbackScope: "evaluation",
		},
	}

	for _, node := range DefaultWorkflowSpec.Nodes {
		expected, ok := want[node.ID]
		if !ok {
			t.Fatalf("unexpected default node %q", node.ID)
		}
		if node.MaxRetries != expected.maxRetries {
			t.Fatalf("%s MaxRetries = %d, want %d", node.ID, node.MaxRetries, expected.maxRetries)
		}
		if !sameFailureClasses(node.RetryOn, expected.retryOn) {
			t.Fatalf("%s RetryOn = %v, want %v", node.ID, node.RetryOn, expected.retryOn)
		}
		if node.Rollback.DefaultUnit.MaxAttempts != expected.rollbackMax {
			t.Fatalf("%s rollback max = %d, want %d", node.ID, node.Rollback.DefaultUnit.MaxAttempts, expected.rollbackMax)
		}
		if node.Rollback.DefaultUnit.Scope != expected.rollbackScope {
			t.Fatalf("%s rollback scope = %q, want %q", node.ID, node.Rollback.DefaultUnit.Scope, expected.rollbackScope)
		}
		if node.Rollback.OnContract != expected.rollbackOnContract {
			t.Fatalf("%s rollback OnContract = %q, want %q", node.ID, node.Rollback.OnContract, expected.rollbackOnContract)
		}
	}
}

func TestWorkflowSpecUnmarshalsLegacyNodeSpecWithoutRetryFields(t *testing.T) {
	var spec WorkflowSpec
	err := json.Unmarshal([]byte(`{"run_id":"legacy","nodes":[{"id":"planning","kind":"planning","depends_on":["clarification"]}]}`), &spec)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	node := spec.Nodes[0]
	if node.MaxRetries != 0 {
		t.Fatalf("MaxRetries = %d, want zero legacy default", node.MaxRetries)
	}
	if len(node.RetryOn) != 0 {
		t.Fatalf("RetryOn = %v, want empty legacy default", node.RetryOn)
	}
	if rollbackSpecEnabled(node.Rollback) {
		t.Fatalf("Rollback = %+v, want disabled legacy zero value", node.Rollback)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameFailureClasses(a, b []FailureClass) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
