package workflow

import (
	"context"
	"encoding/json"
)

type developmentNode struct{}

func (n *developmentNode) Kind() NodeKind { return NodeKindDevelopment }

func (n *developmentNode) PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error) {
	spec := DefaultRollbackSpec(NodeKindDevelopment)
	unit := rctx.Unit
	if unit.Scope == "" {
		unit = spec.DefaultUnit
	}
	var attempts []AttemptEvent
	if rctx.Lineage != nil {
		listed, err := rctx.Lineage.List(AttemptFilter{RunID: rctx.RunID, NodeID: rctx.NodeID, Scope: unit.Scope})
		if err != nil {
			return nil, err
		}
		attempts = rollbackBudgetEvents(listed)
	}
	decision := NewRollbackRunner().Decide(rctx, spec, unit, attempts, signal)
	if signal.Reason != "" {
		decision.Rationale = signal.Reason
	}
	return decision, nil
}

func (n *developmentNode) RollbackArtifacts(rctx RollbackContext) []ArtifactRef {
	return nil
}

func (n *developmentNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	planOutput := req.Inputs["planning"]
	planJSON, ok := planOutput.Values["plan"].(json.RawMessage)
	if !ok {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: "plan not found in inputs"}, nil
	}
	var plan Plan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	state, err := RunDevelopmentOnly(ctx, req.RepoRoot, &plan)
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	subtaskResults := any([]*SubtaskResult{})
	if state.Details != nil {
		if results, ok := state.Details["subtask_results"]; ok {
			subtaskResults = results
		}
	}
	return &NodeResult{
		NodeID: req.Spec.ID,
		Status: NodeStatusCompleted,
		Values: map[string]any{"state": state, "subtask_results": subtaskResults},
	}, nil
}
