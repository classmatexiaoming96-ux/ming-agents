package workflow

import "testing"

func TestDefaultWorkflowSpecMatchesCurrentNodeOrder(t *testing.T) {
	spec := DefaultWorkflowSpec

	want := []struct {
		id      string
		kind    NodeKind
		depends []string
	}{
		{id: "clarification", kind: NodeKindClarification},
		{id: "planning", kind: NodeKindPlanning, depends: []string{"clarification"}},
		{id: "development", kind: NodeKindDevelopment, depends: []string{"planning"}},
		{id: "evaluation", kind: NodeKindEvaluation, depends: []string{"development"}},
		{id: "review", kind: NodeKindReview, depends: []string{"evaluation"}},
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
