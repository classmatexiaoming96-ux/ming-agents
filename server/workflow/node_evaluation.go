package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
)

type evaluationNode struct{}

func (n *evaluationNode) Kind() NodeKind { return NodeKindEvaluation }

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
