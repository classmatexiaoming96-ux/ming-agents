package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
)

type evaluationNode struct{}

var runEvaluationWithPlanForNode = RunEvaluationWithPlan

func (n *evaluationNode) Kind() NodeKind { return NodeKindEvaluation }

func (n *evaluationNode) PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error) {
	unit := rctx.Unit
	if unit.Scope == "" {
		unit = DefaultRollbackSpec(NodeKindEvaluation).DefaultUnit
	}
	action := RollbackActionBlocked
	if evaluationFailureRetryable(signal.FailureClass) {
		action = RollbackActionRetryReport
	}
	decision := NewRollbackRunner().Decide(rctx, RollbackSpec{DefaultUnit: unit, OnContract: action}, unit, nil, RollbackSignal{
		FailureClass: FailureClassContractError,
		Reason:       signal.Reason,
		SourceNode:   signal.SourceNode,
	})
	if signal.Reason != "" {
		decision.Rationale = signal.Reason
	}
	return decision, nil
}

func (n *evaluationNode) RollbackArtifacts(rctx RollbackContext) []ArtifactRef {
	return nil
}

func (n *evaluationNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	plan, err := evaluationPlanFromInputs(req.Inputs)
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	brief, err := InjectBrief(ctx, BriefInjectContext{
		RunID:    req.RunID,
		RepoRoot: req.RepoRoot,
		Kind:     req.Spec.Kind,
		Project:  projectFromRepoRoot(req.RepoRoot),
		Query:    req.Spec.ID,
	})
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	result, err := runEvaluationWithPlanForNode(ctx, req.RepoRoot, req.RunID, plan)
	if err != nil {
		return nodeResultWithBrief(&NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, brief), err
	}
	resultJSON, _ := json.Marshal(result)
	return nodeResultWithBrief(&NodeResult{
		NodeID:      req.Spec.ID,
		Status:      NodeStatusCompleted,
		Values:      map[string]any{"evaluation": json.RawMessage(resultJSON)},
		OutputPaths: []string{filepath.Join(req.RepoRoot, ".workflow", "runs", req.RunID, "evaluation.json")},
	}, brief), nil
}

func evaluationPlanFromInputs(inputs NodeInputs) (*Plan, error) {
	planOutput, ok := inputs["planning"]
	if !ok || planOutput.Values == nil {
		return nil, nil
	}
	planJSON, ok := planOutput.Values["plan"].(json.RawMessage)
	if !ok {
		return nil, nil
	}
	var plan Plan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
