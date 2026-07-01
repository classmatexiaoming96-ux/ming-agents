package workflow

import (
	"context"
	"encoding/json"
)

type developmentNode struct{}

var runDevelopmentOnlyWithMemoryForNode = RunDevelopmentOnlyWithMemory

func (n *developmentNode) Kind() NodeKind { return NodeKindDevelopment }

func (n *developmentNode) PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error) {
	spec := rollbackSpecForContext(NodeKindDevelopment, rctx)
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
	briefs := map[string]*BriefInjectResult{}
	memoryBySubtask := map[string]string{}
	for _, st := range plan.Subtasks {
		brief, err := InjectBrief(ctx, BriefInjectContext{
			RunID:     req.RunID,
			RepoRoot:  req.RepoRoot,
			Kind:      req.Spec.Kind,
			Project:   projectFromRepoRoot(req.RepoRoot),
			Query:     developmentBriefQuery(st),
			AuditName: st.ID,
		})
		if err != nil {
			return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, err
		}
		if brief != nil {
			briefs[st.ID] = brief
			memoryBySubtask[st.ID] = brief.Markdown
		}
	}
	firstBrief := firstBriefResult(briefs, plan.Subtasks)
	state, err := runDevelopmentOnlyWithMemoryForNode(ctx, req.RepoRoot, &plan, memoryBySubtask)
	if err != nil {
		return nodeResultWithBrief(&NodeResult{NodeID: req.Spec.ID, Status: NodeStatusFailed, Error: err.Error()}, firstBrief), err
	}
	subtaskResults := any([]*SubtaskResult{})
	if state.Details != nil {
		if results, ok := state.Details["subtask_results"]; ok {
			subtaskResults = results
		}
	}
	result := &NodeResult{
		NodeID: req.Spec.ID,
		Status: NodeStatusCompleted,
		Values: map[string]any{"state": state, "subtask_results": subtaskResults},
	}
	if len(briefs) > 0 {
		paths := map[string]string{}
		for id, brief := range briefs {
			paths[id] = brief.Path
		}
		result.Values["brief_paths"] = paths
	}
	return nodeResultWithBrief(result, firstBrief), nil
}
