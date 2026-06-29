package workflow

import (
	"context"
	"encoding/json"
)

type developmentNode struct{}

func (n *developmentNode) Kind() NodeKind { return NodeKindDevelopment }

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
