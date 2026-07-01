package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"
)

type NodeExecutor struct {
	registry *NodeRegistry
	services NodeServices
}

func NewNodeExecutor(registry *NodeRegistry, services NodeServices) *NodeExecutor {
	return &NodeExecutor{registry: registry, services: services}
}

func (e *NodeExecutor) Run(ctx context.Context, repoRoot string, spec WorkflowSpec, initial NodeInputs) (map[string]NodeOutput, error) {
	dag, err := BuildNodeDAG(spec)
	if err != nil {
		return nil, err
	}
	order, err := dag.TopologicalSort()
	if err != nil {
		return nil, err
	}

	specByID := make(map[string]NodeSpec, len(spec.Nodes))
	statuses := make(map[string]NodeStatus, len(spec.Nodes))
	for _, ns := range spec.Nodes {
		specByID[ns.ID] = ns
		statuses[ns.ID] = NodeStatusPending
	}

	runID := spec.RunID
	if runID == "" {
		runID = time.Now().Format("20060102-150405")
	}

	outputs := make(map[string]NodeOutput, len(initial)+len(spec.Nodes))
	for nodeID, output := range initial {
		outputs[nodeID] = output
	}

	for _, dagNode := range order {
		ns, ok := specByID[dagNode.ID]
		if !ok {
			return outputs, fmt.Errorf("workflow spec missing node %q", dagNode.ID)
		}
		node, err := e.registry.Resolve(ns.Kind)
		if err != nil {
			return outputs, err
		}

		inputs := NodeInputs{}
		if input, ok := outputs["input"]; ok {
			inputs["input"] = input
		}
		for _, dep := range ns.DependsOn {
			out, ok := outputs[dep]
			if !ok {
				return outputs, fmt.Errorf("node %s dependency %s has no output", ns.ID, dep)
			}
			inputs[dep] = out
		}

		result, err := e.executeNodeWithRetry(ctx, repoRoot, runID, ns, node, inputs, statuses)
		if result == nil {
			return outputs, err
		}

		output := nodeOutputFromResult(ns.ID, result)
		normalizeDevelopmentOutput(ns, output)
		outputs[ns.ID] = output
		statuses[ns.ID] = result.Status
		e.writeState(repoRoot, runID, statuses, nodeStateDetails(result))

		if err != nil {
			return outputs, fmt.Errorf("node %s (%s): %w", ns.ID, ns.Kind, err)
		}
		if result.Status == NodeStatusFailed {
			return outputs, fmt.Errorf("node %s (%s) failed: %s", ns.ID, ns.Kind, result.Error)
		}
		if result.Status == NodeStatusBlocked {
			return outputs, fmt.Errorf("node %s (%s) blocked: %s", ns.ID, ns.Kind, result.Error)
		}
	}

	return outputs, nil
}

func (e *NodeExecutor) executeNodeWithRetry(ctx context.Context, repoRoot, runID string, ns NodeSpec, node WorkflowNode, inputs NodeInputs, statuses map[string]NodeStatus) (*NodeResult, error) {
	maxRetries := ns.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	var lastResult *NodeResult
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		statuses[ns.ID] = NodeStatusRunning
		e.writeState(repoRoot, runID, statuses, map[string]any{
			"node_id":       ns.ID,
			"attempt_count": attempt + 1,
		})

		result, err := node.Execute(ctx, NodeRequest{
			RunID:    runID,
			RepoRoot: repoRoot,
			Spec:     ns,
			Config:   nodeRequestConfig(ns),
			Inputs:   inputs,
			Services: e.services,
		})
		lastResult, lastErr = result, err
		if result != nil {
			failureClass := failureClassForAttempt(result, err)
			fillNodeResultRuntimeFields(result, attempt+1, failureClass, attempt >= maxRetries && nodeAttemptFailed(result, err) && shouldRetryNode(ns, failureClass))
		}
		if !nodeAttemptFailed(result, err) {
			return result, nil
		}
		failureClass := failureClassForAttempt(result, err)
		decision, rollbackErr := e.prepareRollbackDecision(ctx, repoRoot, runID, ns, node, result, failureClass, attempt+1)
		if rollbackErr != nil {
			lastErr = rollbackErr
			break
		}
		if result != nil && decision != nil {
			applyRollbackDecisionToResult(result, decision)
		}
		if attempt < maxRetries && shouldRetryNode(ns, failureClass) {
			continue
		}
		break
	}
	if lastResult == nil {
		statuses[ns.ID] = NodeStatusFailed
		e.writeState(repoRoot, runID, statuses, map[string]any{
			"node_id":         ns.ID,
			"error":           "node returned nil result",
			"failure_class":   failureClassForAttempt(nil, lastErr),
			"next_action":     NextActionForFailure(failureClassForAttempt(nil, lastErr)),
			"retry_exhausted": shouldRetryNode(ns, failureClassForAttempt(nil, lastErr)),
			"attempt_count":   maxRetries + 1,
		})
		if lastErr != nil {
			return nil, fmt.Errorf("node %s (%s): %w", ns.ID, ns.Kind, lastErr)
		}
		return nil, fmt.Errorf("node %s returned nil result", ns.ID)
	}
	return lastResult, lastErr
}

func (e *NodeExecutor) prepareRollbackDecision(ctx context.Context, repoRoot, runID string, ns NodeSpec, node WorkflowNode, result *NodeResult, failureClass FailureClass, attemptCount int) (*RollbackDecision, error) {
	rbNode, ok := node.(RollbackCapableNode)
	if !ok || !rollbackSpecEnabled(ns.Rollback) {
		return nil, nil
	}
	rctx := executorRollbackContext(repoRoot, runID, ns)
	signal := RollbackSignal{
		FailureClass:  failureClass,
		Reason:        rollbackReason(result),
		SourceNode:    ns.ID,
		SourceAttempt: attemptCount,
	}
	return rbNode.PrepareRollback(ctx, rctx, signal)
}

func rollbackSpecEnabled(spec RollbackSpec) bool {
	return spec.DefaultUnit.Scope != "" ||
		spec.DefaultUnit.MaxAttempts > 0 ||
		spec.DefaultUnit.ReusePolicy != "" ||
		spec.OnContract != "" ||
		spec.OnHumanReject != "" ||
		spec.OnProductDefect != ""
}

func executorRollbackContext(repoRoot, runID string, ns NodeSpec) RollbackContext {
	spec := ns.Rollback
	unit := spec.DefaultUnit
	return RollbackContext{
		RunID:    runID,
		NodeID:   ns.ID,
		NodeKind: ns.Kind,
		Unit:     unit,
		Budget: RollbackBudget{
			MaxAttempts:     unit.MaxAttempts,
			ExhaustedAction: RollbackActionBlocked,
		},
		Lineage: NewFileLineageStore(repoRoot),
	}
}

func rollbackReason(result *NodeResult) string {
	if result == nil {
		return "node returned nil result"
	}
	if result.RetryAdvice != "" {
		return result.RetryAdvice
	}
	if result.Error != "" {
		return result.Error
	}
	return string(result.Status)
}

func applyRollbackDecisionToResult(result *NodeResult, decision *RollbackDecision) {
	if result.RetryAdvice == "" {
		result.RetryAdvice = decision.Rationale
	}
	if result.NextAction == "" {
		result.NextAction = nextActionForRollbackDecision(decision.Action)
	}
}

func nextActionForRollbackDecision(action RollbackAction) string {
	switch action {
	case RollbackActionRetrySubtask:
		return string(NextActionRetrySubtask)
	case RollbackActionFixEnvironment:
		return string(NextActionFixEnvironment)
	case RollbackActionAskUser:
		return string(NextActionAskUser)
	case RollbackActionRegenerateSubtask:
		return string(NextActionRegenerateSubtask)
	case RollbackActionRetryReport:
		return string(NextActionRetryReport)
	case RollbackActionRetryGenerator:
		return string(NextActionRetryGenerator)
	case RollbackActionFixClarification, RollbackActionRegeneratePlan:
		return "retry_generator"
	case RollbackActionBlocked:
		return "blocked"
	default:
		return ""
	}
}

func nodeAttemptFailed(result *NodeResult, err error) bool {
	if err != nil || result == nil {
		return true
	}
	return result.Status == NodeStatusFailed || result.Status == NodeStatusBlocked
}

func fillNodeResultRuntimeFields(result *NodeResult, attemptCount int, failureClass FailureClass, retryExhausted bool) {
	if result.AttemptCount == 0 {
		result.AttemptCount = attemptCount
	}
	if failureClass != "" && result.FailureClass == "" {
		result.FailureClass = failureClass
	}
	if retryExhausted && shouldExposeRetryExhausted(failureClass) {
		result.RetryExhausted = true
	}
	if result.NextAction == "" && shouldExposeNextAction(failureClass) {
		result.NextAction = NextActionForFailure(failureClass)
	}
}

func failureClassForAttempt(result *NodeResult, err error) FailureClass {
	if result != nil && result.FailureClass != "" {
		return result.FailureClass
	}
	if err != nil {
		if errorsIsContextDone(err) {
			return FailureClassUserBlocked
		}
		return FailureClassTransient
	}
	if result != nil && result.Status == NodeStatusBlocked {
		return FailureClassUserBlocked
	}
	if result != nil && result.Status == NodeStatusFailed {
		return FailureClassInconclusive
	}
	return FailureClassNone
}

func errorsIsContextDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func shouldRetryNode(ns NodeSpec, failureClass FailureClass) bool {
	if failureClass == "" || failureClass == FailureClassNone {
		return false
	}
	return slices.Contains(ns.RetryOn, failureClass)
}

func shouldExposeRetryExhausted(failureClass FailureClass) bool {
	return failureClass != "" && failureClass != FailureClassNone
}

func shouldExposeNextAction(failureClass FailureClass) bool {
	return failureClass != "" && failureClass != FailureClassNone
}

func nodeRequestConfig(ns NodeSpec) map[string]any {
	config := map[string]any{}
	for k, v := range ns.Config {
		config[k] = v
	}
	return config
}

func BuildNodeDAG(spec WorkflowSpec) (*DAG, error) {
	dag := NewDAG()

	for _, node := range spec.Nodes {
		dag.AddNode(&Node{
			ID:     node.ID,
			Name:   node.ID,
			Type:   string(node.Kind),
			Inputs: map[string]string{},
		})
	}

	for _, node := range spec.Nodes {
		for _, dep := range node.DependsOn {
			if err := dag.AddEdge(dep, node.ID); err != nil {
				return nil, err
			}
		}
	}

	if dag.DetectCycle() {
		return nil, fmt.Errorf("workflow spec contains a dependency cycle")
	}
	return dag, nil
}

func nodeOutputFromResult(nodeID string, result *NodeResult) NodeOutput {
	outputs := map[string]string{}
	for _, path := range result.OutputPaths {
		if path == "" {
			continue
		}
		outputs[filepath.Base(path)] = path
		outputs[nodeID+"_output"] = path
	}
	return NodeOutput{
		NodeID:  nodeID,
		Values:  result.Values,
		Outputs: outputs,
	}
}

func nodeStateDetails(result *NodeResult) map[string]any {
	details := map[string]any{
		"node_id":       result.NodeID,
		"error":         result.Error,
		"blocked_items": result.BlockedItems,
		"output_paths":  result.OutputPaths,
	}
	if result.FailureClass != "" {
		details["failure_class"] = result.FailureClass
	}
	if result.RetryAdvice != "" {
		details["retry_advice"] = result.RetryAdvice
	}
	if result.NextAction != "" {
		details["next_action"] = result.NextAction
	}
	if result.RetryExhausted {
		details["retry_exhausted"] = result.RetryExhausted
	}
	if len(result.ArtifactRefs) > 0 {
		details["artifact_refs"] = result.ArtifactRefs
	}
	if result.AttemptCount > 0 {
		details["attempt_count"] = result.AttemptCount
	}
	return details
}

func normalizeDevelopmentOutput(spec NodeSpec, output NodeOutput) {
	if spec.Kind != NodeKindDevelopment || output.Values == nil {
		return
	}
	state, _ := output.Values["state"].(*WorkflowState)
	if state == nil {
		return
	}
	if state.Details == nil {
		state.Details = map[string]any{}
	}
	results, ok := output.Values["subtask_results"]
	if !ok {
		results = state.Details["subtask_results"]
	}
	if results == nil {
		results = []*SubtaskResult{}
	}
	data, err := json.Marshal(results)
	if err != nil {
		return
	}
	state.Details["subtask_results"] = json.RawMessage(data)
	output.Values["subtask_results"] = json.RawMessage(data)
}

func (e *NodeExecutor) writeState(repoRoot, runID string, nodes map[string]NodeStatus, details map[string]any) {
	if e.services.StatusWriter == nil {
		return
	}
	snapshot := make(map[string]NodeStatus, len(nodes))
	for nodeID, status := range nodes {
		snapshot[nodeID] = status
	}
	_ = e.services.StatusWriter.WriteState(repoRoot, runID, snapshot, details)
}
