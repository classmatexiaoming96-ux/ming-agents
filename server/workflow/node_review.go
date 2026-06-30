package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
)

type reviewNode struct{}

func (n *reviewNode) Kind() NodeKind { return NodeKindReview }

func (n *reviewNode) PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error) {
	spec := DefaultRollbackSpec(NodeKindReview)
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

func (n *reviewNode) RollbackArtifacts(rctx RollbackContext) []ArtifactRef {
	return nil
}

func (n *reviewNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	devOutput := req.Inputs["development"]
	state, ok := devOutput.Values["state"].(*WorkflowState)
	if !ok {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: "state not found in development output"}, nil
	}

	planOutput := req.Inputs["planning"]
	planJSON, ok := planOutput.Values["plan"].(json.RawMessage)
	if !ok {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: "plan not found in inputs"}, nil
	}
	var plan Plan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}

	results, ok := subtaskResultsFromState(state)
	if !ok {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: "subtask results not found in development output"}, nil
	}

	runRoot := filepath.Join(req.RepoRoot, ".workflow", "runs", req.RunID)
	nodeDir := filepath.Join(runRoot, req.Spec.ID)
	diffFile, _, err := writeReviewInputs(req.RepoRoot, nodeDir, &plan, results)
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	subtaskReports := map[string]*ReviewReport{}
	reviewOutBySubtask := map[string]string{}
	for _, result := range results {
		if result == nil {
			continue
		}
		subtaskReport, reviewOut, _, err := RunSubtaskReview(ctx, req.RepoRoot, req.RunID, &plan, result, diffFile)
		if err != nil {
			return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
		}
		subtaskReports[result.Subtask.ID] = subtaskReport
		reviewOutBySubtask[result.Subtask.ID] = reviewOut
	}
	aggregateReport, aggregateOut, _, err := RunAggregateReview(ctx, req.RepoRoot, req.RunID, &plan, subtaskReports)
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
	}
	report := MergeReviewReports(subtaskReports, aggregateReport)
	reviewOutBySubtask["aggregate"] = aggregateOut
	return &NodeResult{
		NodeID: req.Spec.ID,
		Status: NodeStatusCompleted,
		Values: map[string]any{"report": report, "review_out": reviewOutBySubtask},
	}, nil
}

func subtaskResultsFromState(state *WorkflowState) ([]*SubtaskResult, bool) {
	if state == nil || state.Details == nil {
		return nil, false
	}
	value, ok := state.Details["subtask_results"]
	if !ok {
		return nil, false
	}
	if results, ok := value.([]*SubtaskResult); ok {
		return results, true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var results []*SubtaskResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, false
	}
	return results, true
}
