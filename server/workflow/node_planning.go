package workflow

import (
	"context"
	"encoding/json"
	"log"
)

type planningNode struct{}

func (n *planningNode) Kind() NodeKind { return NodeKindPlanning }

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
