package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
)

type evaluationNode struct{}

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
	result, err := RunEvaluation(ctx, req.RepoRoot, req.RunID)
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	resultJSON, _ := json.Marshal(result)
	return &NodeResult{
		NodeID:      req.Spec.ID,
		Status:      NodeStatusCompleted,
		Values:      map[string]any{"evaluation": json.RawMessage(resultJSON)},
		OutputPaths: []string{filepath.Join(req.RepoRoot, ".workflow", "runs", req.RunID, "evaluation.json")},
	}, nil
}
