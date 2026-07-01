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

type rollbackRecordingNode struct {
	kind         NodeKind
	result       *NodeResult
	executeCalls *int
	prepareCalls *int
	prepare      func(RollbackContext, RollbackSignal) *RollbackDecision
}

func (n rollbackRecordingNode) Kind() NodeKind { return n.kind }

func (n rollbackRecordingNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	*n.executeCalls = *n.executeCalls + 1
	return n.result, nil
}

func (n rollbackRecordingNode) PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error) {
	*n.prepareCalls = *n.prepareCalls + 1
	if n.prepare != nil {
		return n.prepare(rctx, signal), nil
	}
	return &RollbackDecision{Action: RollbackActionRetryReport, TargetScope: rctx.Unit.Scope}, nil
}

func (n rollbackRecordingNode) RollbackArtifacts(rctx RollbackContext) []ArtifactRef {
	return []ArtifactRef{{Type: ArtifactTypeReviewReport, Path: "review.out.md"}}
}

type recordingStatusWriter struct {
	states      []map[string]NodeStatus
	lastDetails map[string]any
	phase       *PhaseStatus
}

func (w *recordingStatusWriter) WriteState(repoRoot, runID string, nodes map[string]NodeStatus, details map[string]any) error {
	snapshot := make(map[string]NodeStatus, len(nodes))
	for nodeID, status := range nodes {
		snapshot[nodeID] = status
	}
	w.states = append(w.states, snapshot)
	w.lastDetails = details
	return nil
}

func (w *recordingStatusWriter) WritePhase(runID string, status *PhaseStatus) error {
	w.phase = status
	return nil
}

func TestNodeExecutorSkipsRollbackRunnerWhenSpecDisabled(t *testing.T) {
	var executeCalls, prepareCalls int
	registry := NewNodeRegistry()
	registry.Register("rollback-capable", func() WorkflowNode {
		return rollbackRecordingNode{
			kind:         "rollback-capable",
			result:       &NodeResult{NodeID: "review", Status: NodeStatusFailed, Error: "bad report", FailureClass: FailureClassContractError},
			executeCalls: &executeCalls,
			prepareCalls: &prepareCalls,
		}
	})

	_, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), "/repo", WorkflowSpec{
		RunID: "run-rollback-disabled",
		Nodes: []NodeSpec{{
			ID:   "review",
			Kind: "rollback-capable",
		}},
	}, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want node failure")
	}
	if executeCalls != 1 {
		t.Fatalf("Execute calls = %d, want 1", executeCalls)
	}
	if prepareCalls != 0 {
		t.Fatalf("PrepareRollback calls = %d, want 0", prepareCalls)
	}
}

func TestNodeExecutorWritesPhaseStatusForExhaustedRollbackDecision(t *testing.T) {
	var executeCalls, prepareCalls int
	writer := &recordingStatusWriter{}
	spec := RollbackSpec{
		DefaultUnit: RollbackUnit{Scope: "review", MaxAttempts: 1, ReusePolicy: SessionReuseSameSession},
		OnContract:  RollbackActionRetryReport,
	}
	registry := NewNodeRegistry()
	registry.Register(NodeKindReview, func() WorkflowNode {
		return rollbackRecordingNode{
			kind:         NodeKindReview,
			result:       &NodeResult{NodeID: "review", Status: NodeStatusFailed, Error: "bad report", FailureClass: FailureClassContractError},
			executeCalls: &executeCalls,
			prepareCalls: &prepareCalls,
			prepare: func(rctx RollbackContext, signal RollbackSignal) *RollbackDecision {
				return NewRollbackRunner().Decide(rctx, rctx.Spec, rctx.Unit, []AttemptEvent{{Scope: "review", Attempt: 1}}, signal)
			},
		}
	})

	_, err := NewNodeExecutor(registry, NodeServices{StatusWriter: writer}).Run(context.Background(), "/repo", WorkflowSpec{
		RunID: "run-exhausted",
		Nodes: []NodeSpec{{
			ID:       "review",
			Kind:     NodeKindReview,
			Rollback: spec,
		}},
	}, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want exhausted node failure")
	}
	if executeCalls != 1 {
		t.Fatalf("Execute calls = %d, want 1", executeCalls)
	}
	if prepareCalls != 1 {
		t.Fatalf("PrepareRollback calls = %d, want 1", prepareCalls)
	}
	if writer.phase == nil {
		t.Fatal("WritePhase was not called")
	}
	if writer.phase.FailureClass != FailureClassContractError {
		t.Fatalf("PhaseStatus FailureClass = %q, want %q", writer.phase.FailureClass, FailureClassContractError)
	}
	if writer.phase.NextAction != NextActionForFailure(FailureClassContractError) {
		t.Fatalf("PhaseStatus NextAction = %q, want %q", writer.phase.NextAction, NextActionForFailure(FailureClassContractError))
	}
	if got := writer.lastDetails["retry_exhausted"]; got != true {
		t.Fatalf("state retry_exhausted = %v, want true", got)
	}
	if got := writer.lastDetails["attempt_count"]; got != 1 {
		t.Fatalf("state attempt_count = %v, want 1", got)
	}
}

func TestNodeExecutorMaxRetriesZeroDoesNotRetry(t *testing.T) {
	var attempts int
	registry := NewNodeRegistry()
	registry.Register("single-shot", func() WorkflowNode {
		return sequenceNode{
			kind:  "single-shot",
			calls: &attempts,
			results: []*NodeResult{
				{NodeID: "single-shot", Status: NodeStatusFailed, Error: "temporary", FailureClass: FailureClassTransient},
				{NodeID: "single-shot", Status: NodeStatusCompleted},
			},
		}
	})

	_, err := NewNodeExecutor(registry, NodeServices{}).Run(context.Background(), "/repo", WorkflowSpec{
		RunID: "run-no-retry",
		Nodes: []NodeSpec{{
			ID:         "single-shot",
			Kind:       "single-shot",
			MaxRetries: 0,
			RetryOn:    []FailureClass{FailureClassTransient},
		}},
	}, nil)
	if err == nil {
		t.Fatal("Run() error = nil, want first attempt failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
