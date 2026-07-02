package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
)

type reviewNode struct{}

var runSubtaskReviewWithMemoryForNode = RunSubtaskReviewWithMemory
var runAggregateReviewWithMemoryForNode = RunAggregateReviewWithMemory

func (n *reviewNode) Kind() NodeKind { return NodeKindReview }

func (n *reviewNode) PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error) {
	spec := rollbackSpecForContext(NodeKindReview, rctx)
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
	brief, err := InjectBrief(ctx, BriefInjectContext{
		RunID:    req.RunID,
		RepoRoot: req.RepoRoot,
		Kind:     req.Spec.Kind,
		Project:  projectFromRepoRoot(req.RepoRoot),
		Query:    reviewBriefQuery(&plan, results),
	})
	if err != nil {
		return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
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
		subtaskReport, reviewOut, _, err := runSubtaskReviewWithMemoryForNode(ctx, req.RepoRoot, req.RunID, &plan, result, diffFile, briefMarkdown(brief))
		if err != nil {
			return nodeResultWithBrief(&NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, brief), err
		}
		subtaskReports[result.Subtask.ID] = subtaskReport
		reviewOutBySubtask[result.Subtask.ID] = reviewOut
	}
	aggregateReport, aggregateOut, _, err := runAggregateReviewWithMemoryForNode(ctx, req.RepoRoot, req.RunID, &plan, subtaskReports, briefMarkdown(brief))
	if err != nil {
		return nodeResultWithBrief(&NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, brief), err
	}
	report := MergeReviewReports(subtaskReports, aggregateReport)
	reviewOutBySubtask["aggregate"] = aggregateOut
	// Close the brief -> output -> score loop: the review turns share one brief,
	// so score its injected memories against the aggregate review output.
	applyImplicitFeedback(brief, aggregateOut)
	mirrorBriefAuditToRunBundle(req, brief, "")
	mirrorReuseAckFileToRunBundle(req, string(req.Spec.Kind))
	return nodeResultWithBrief(&NodeResult{
		NodeID: req.Spec.ID,
		Status: NodeStatusCompleted,
		Values: map[string]any{"report": report, "review_out": reviewOutBySubtask},
	}, brief), nil
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
