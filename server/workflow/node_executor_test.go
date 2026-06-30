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
