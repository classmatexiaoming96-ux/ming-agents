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
	runDevelopment := RunDevelopment
	if skipInternalReviewEvaluation(req.Config) {
		runDevelopment = RunDevelopmentOnly
	}
	state, err := runDevelopment(ctx, req.RepoRoot, &plan)
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	return &NodeResult{
		NodeID: req.Spec.ID,
		Status: NodeStatusCompleted,
		Values: map[string]any{"state": state},
	}, nil
}

func skipInternalReviewEvaluation(config map[string]any) bool {
	value, ok := config[ConfigSkipInternalReviewEvaluation]
	if !ok {
		return false
	}
	skip, ok := value.(bool)
	return ok && skip
}
