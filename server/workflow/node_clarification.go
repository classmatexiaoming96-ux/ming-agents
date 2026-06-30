package workflow

import (
	"context"
	"log"
)

type clarificationNode struct{}

func (n *clarificationNode) Kind() NodeKind { return NodeKindClarification }

func (n *clarificationNode) PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error) {
	spec := DefaultRollbackSpec(NodeKindClarification)
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
		decision.Rationale = renderClarificationRevisionPrompt("", "", signal.Reason)
	}
	return decision, nil
}

func (n *clarificationNode) RollbackArtifacts(rctx RollbackContext) []ArtifactRef {
	return nil
}

func (n *clarificationNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	userInputRaw, ok := req.Inputs["input"].Values["user_input"]
	if !ok {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: "user_input not found in inputs"}, nil
	}
	userInput, ok := userInputRaw.(string)
	if !ok {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: "user_input is not a string"}, nil
	}
	runID := req.RunID
	var reusePath string
	if runID != "" {
		query := userInput
		if query == "" {
			query = req.Spec.ID
		}
		memories, err := recallReuseHits(query, 10)
		if err != nil {
			return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
		}
		reusePath, err = writeReuseMarkdown(req.RepoRoot, runID, string(req.Spec.Kind), memories)
		if err != nil {
			return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
		}
		ack := ReuseAck{
			Accepted: true,
			Applied:  memories,
			Note:     "bootstrap ack from clarification node",
		}
		if err := WriteReuseAckAt(ctx, req.RepoRoot, runID, "clarification", ack); err != nil {
			log.Printf("WriteReuseAckAt failed: %v", err)
		}
	}
	outputPath, err := RunClarification(ctx, req.RepoRoot, userInput)
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	paths := []string{outputPath}
	values := map[string]any{}
	if reusePath != "" {
		paths = append(paths, reusePath)
		values["reuse_path"] = reusePath
	}
	return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusCompleted, Values: values, OutputPaths: paths}, nil
}
