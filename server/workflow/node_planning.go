package workflow

import (
	"context"
	"encoding/json"
	"log"
)

type planningNode struct{}

func (n *planningNode) Kind() NodeKind { return NodeKindPlanning }

func (n *planningNode) PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error) {
	spec := DefaultRollbackSpec(NodeKindPlanning)
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
		decision.Rationale = renderPlanningRevisionPrompt("", "", signal.Reason)
	}
	return decision, nil
}

func (n *planningNode) RollbackArtifacts(rctx RollbackContext) []ArtifactRef {
	return nil
}

func (n *planningNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	if req.RunID != "" {
		accepted, err := CheckReuseAckAt(ctx, req.RepoRoot, req.RunID, string(req.Spec.Kind))
		if err != nil {
			return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
		}
		if !accepted {
			// Phase 0 compatibility: warn without blocking. After Phase 3,
			// planning can restore hard gating once it writes planning-brief.json.
			log.Printf("warning: reuse-ack not found for run=%s phase=%s, proceeding (Phase 0 compat)",
				req.RunID, req.Spec.Kind)
		}
	}
	clarOutput := req.Inputs["clarification"]
	clarFile := clarOutput.Outputs["clarification_output"]
	plan, err := RunPlanning(ctx, req.RepoRoot, clarFile)
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	planJSON, _ := json.Marshal(plan)
	return &NodeResult{
		NodeID: req.Spec.ID,
		Status: NodeStatusCompleted,
		Values: map[string]any{"plan": json.RawMessage(planJSON)},
	}, nil
}
