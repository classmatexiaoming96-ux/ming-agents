package workflow

import (
	"context"
	"reflect"
	"testing"
)

type recordingNode struct {
	kind NodeKind
	seen *[]string
}

func (n recordingNode) Kind() NodeKind { return n.kind }

func (n recordingNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	*n.seen = append(*n.seen, req.Spec.ID)
	values := map[string]any{"node_id": req.Spec.ID}
	if input, ok := req.Inputs["input"].Values["user_input"].(string); ok {
		values["user_input"] = input
	}
	for depID, dep := range req.Inputs {
		if depID == "input" {
			continue
		}
		values["from_"+depID] = dep.Values["node_id"]
	}
	return &NodeResult{
		NodeID:      req.Spec.ID,
		Status:      NodeStatusCompleted,
		Values:      values,
		OutputPaths: []string{"/tmp/" + req.Spec.ID + ".out"},
	}, nil
}

type blockingNode struct{}

func (blockingNode) Kind() NodeKind { return "blocking" }
func (blockingNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusBlocked, Error: "waiting for reuse ack"}, nil
}

type sequenceNode struct {
	kind    NodeKind
	results []*NodeResult
	calls   *int
}

func (n sequenceNode) Kind() NodeKind { return n.kind }

func (n sequenceNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	*n.calls = *n.calls + 1
	index := *n.calls - 1
	if index >= len(n.results) {
		index = len(n.results) - 1
	}
	return n.results[index], nil
}

func TestNodeExecutorRunsNodesInTopologicalOrderAndPassesOutputs(t *testing.T) {
	var seen []string
	registry := NewNodeRegistry()
	registry.Register("first", func() WorkflowNode { return recordingNode{kind: "first", seen: &seen} })
	registry.Register("second", func() WorkflowNode { return recordingNode{kind: "second", seen: &seen} })
	registry.Register("third", func() WorkflowNode { return recordingNode{kind: "third", seen: &seen} })

	spec := WorkflowSpec{
		RunID: "run-test",
		Nodes: []NodeSpec{
			{ID: "third", Kind: "third", DependsOn: []string{"second"}},
			{ID: "first", Kind: "first"},
			{ID: "second", Kind: "second", DependsOn: []string{"first"}},
		},
	}
	initial := NodeInputs{
		"input": {
			NodeID: "input",
			Values: map[string]any{"user_input": "build it"},
		},
	}

	outputs, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), "/repo", spec, initial)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if want := []string{"first", "second", "third"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("execution order = %v, want %v", seen, want)
	}
	if got := outputs["third"].Values["from_second"]; got != "second" {
		t.Fatalf("third input from second = %v, want second", got)
	}
	if got := outputs["first"].Values["user_input"]; got != "build it" {
		t.Fatalf("first input user_input = %v, want build it", got)
	}
	if got := outputs["first"].Outputs["first_output"]; got != "/tmp/first.out" {
		t.Fatalf("first output path = %v, want /tmp/first.out", got)
	}
}

func TestNodeExecutorRetriesTransientFailureForCurrentNode(t *testing.T) {
	var attempts int
	registry := NewNodeRegistry()
	registry.Register("flaky", func() WorkflowNode {
		return sequenceNode{
			kind:  "flaky",
			calls: &attempts,
			results: []*NodeResult{
				{NodeID: "flaky", Status: NodeStatusFailed, Error: "temporary", FailureClass: FailureClassTransient},
				{NodeID: "flaky", Status: NodeStatusCompleted, Values: map[string]any{"ok": true}},
			},
		}
	})

	outputs, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), "/repo", WorkflowSpec{
		RunID: "run-retry",
		Nodes: []NodeSpec{{
			ID:         "flaky",
			Kind:       "flaky",
			MaxRetries: 1,
			RetryOn:    []FailureClass{FailureClassTransient},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := outputs["flaky"].Values["ok"]; got != true {
		t.Fatalf("output ok = %v, want true", got)
	}
}

func TestNodeExecutorDoesNotRetryWhenFailureClassDoesNotMatch(t *testing.T) {
	var attempts int
	registry := NewNodeRegistry()
	registry.Register("invalid", func() WorkflowNode {
		return sequenceNode{
			kind:  "invalid",
			calls: &attempts,
			results: []*NodeResult{
				{NodeID: "invalid", Status: NodeStatusFailed, Error: "bad request", FailureClass: FailureClassInvalidInput},
				{NodeID: "invalid", Status: NodeStatusCompleted},
			},
		}
	})

	_, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), "/repo", WorkflowSpec{
		RunID: "run-invalid",
		Nodes: []NodeSpec{{
			ID:         "invalid",
			Kind:       "invalid",
			MaxRetries: 2,
			RetryOn:    []FailureClass{FailureClassTransient},
		}},
	}, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want invalid input failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestNodeExecutorDefaultPolicyDoesNotRetryEnvironmentOrProductDefect(t *testing.T) {
	tests := []struct {
		name         string
		spec         NodeSpec
		failureClass FailureClass
	}{
		{
			name:         "environment_block",
			spec:         defaultWorkflowNode("evaluation", NodeKindEvaluation, nil),
			failureClass: FailureClassEnvironmentBlock,
		},
		{
			name:         "product_defect",
			spec:         defaultWorkflowNode("review", NodeKindReview, nil),
			failureClass: FailureClassProductDefect,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts int
			registry := NewNodeRegistry()
			registry.Register(tt.spec.Kind, func() WorkflowNode {
				return sequenceNode{
					kind:  tt.spec.Kind,
					calls: &attempts,
					results: []*NodeResult{
						{NodeID: tt.spec.ID, Status: NodeStatusFailed, Error: string(tt.failureClass), FailureClass: tt.failureClass},
						{NodeID: tt.spec.ID, Status: NodeStatusCompleted},
					},
				}
			})

			_, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), "/repo", WorkflowSpec{
				RunID: "run-" + tt.name,
				Nodes: []NodeSpec{tt.spec},
			}, nil)
			if err == nil {
				t.Fatal("Run() error = nil, want failure")
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestNodeExecutorRetryDoesNotRerunCompletedUpstreamNodes(t *testing.T) {
	var firstCalls, secondCalls int
	registry := NewNodeRegistry()
	registry.Register("first", func() WorkflowNode {
		return sequenceNode{
			kind:  "first",
			calls: &firstCalls,
			results: []*NodeResult{
				{NodeID: "first", Status: NodeStatusCompleted, Values: map[string]any{"node_id": "first"}},
			},
		}
	})
	registry.Register("second", func() WorkflowNode {
		return sequenceNode{
			kind:  "second",
			calls: &secondCalls,
			results: []*NodeResult{
				{NodeID: "second", Status: NodeStatusFailed, Error: "temporary", FailureClass: FailureClassTransient},
				{NodeID: "second", Status: NodeStatusCompleted, Values: map[string]any{"node_id": "second"}},
			},
		}
	})

	_, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), "/repo", WorkflowSpec{
		RunID: "run-upstream",
		Nodes: []NodeSpec{
			{ID: "first", Kind: "first"},
			{ID: "second", Kind: "second", DependsOn: []string{"first"}, MaxRetries: 1, RetryOn: []FailureClass{FailureClassTransient}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if firstCalls != 1 {
		t.Fatalf("first calls = %d, want 1", firstCalls)
	}
	if secondCalls != 2 {
		t.Fatalf("second calls = %d, want 2", secondCalls)
	}
}

func TestNodeExecutorStopsOnBlockedNode(t *testing.T) {
	registry := NewNodeRegistry()
	registry.Register("blocking", func() WorkflowNode { return blockingNode{} })

	_, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), "/repo", WorkflowSpec{
		RunID: "run-blocked",
		Nodes: []NodeSpec{
			{ID: "planning", Kind: "blocking"},
		},
	}, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want blocked error")
	}
}
