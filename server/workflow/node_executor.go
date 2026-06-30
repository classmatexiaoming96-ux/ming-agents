package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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

		statuses[ns.ID] = NodeStatusRunning
		result, err := node.Execute(ctx, NodeRequest{
			RunID:    runID,
			RepoRoot: repoRoot,
			Spec:     ns,
			Config:   nodeRequestConfig(ns),
			Inputs:   inputs,
			Services: e.services,
		})
		if result == nil {
			statuses[ns.ID] = NodeStatusFailed
			e.writeState(repoRoot, runID, statuses, map[string]any{"error": "node returned nil result"})
			return outputs, fmt.Errorf("node %s returned nil result", ns.ID)
		}

		output := nodeOutputFromResult(ns.ID, result)
		normalizeDevelopmentOutput(ns, output)
		outputs[ns.ID] = output
		statuses[ns.ID] = result.Status
		e.writeState(repoRoot, runID, statuses, map[string]any{
			"node_id":       result.NodeID,
			"error":         result.Error,
			"blocked_items": result.BlockedItems,
			"output_paths":  result.OutputPaths,
		})

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
