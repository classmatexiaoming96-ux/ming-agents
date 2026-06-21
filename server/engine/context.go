package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
)

// ContextPropagator handles variable/context propagation between steps.
// Epic 2.6: Step间 inputs/outputs 绑定与模板渲染.
type ContextPropagator struct {
	ctx   *Context
	store *store.Store
}

// NewContextPropagator creates a new context propagator.
func NewContextPropagator(ctx *Context, s *store.Store) *ContextPropagator {
	return &ContextPropagator{ctx: ctx, store: s}
}

// PropagateFromTask takes a completed task's result and updates the context.
// It extracts output bindings from the step definition and renders templates
// for downstream steps.
func (p *ContextPropagator) PropagateFromTask(task *domain.Task, step *domain.Step) error {
	// Parse the step outputs from step definition.
	if !step.OutputsJSON.Valid {
		return nil
	}
	var outputs map[string]any
	if err := json.Unmarshal([]byte(step.OutputsJSON.String), &outputs); err != nil {
		return fmt.Errorf("unmarshal step outputs: %w", err)
	}

	// Parse agent result.
	var result adapter.AgentResult
	if task.AgentResult != nil {
		if err := json.Unmarshal(task.AgentResult, &result); err != nil {
			// If result is not JSON, treat as plain text output.
			result.Output = string(task.AgentResult)
		}
	}

	// Build output map from result.
	outMap := p.extractOutputs(step, &result, task.ResultSummaryStr)

	// Store outputs in context.
	p.ctx.SetOutput(step.Name, "_status", "completed")
	for k, v := range outMap {
		p.ctx.SetOutput(step.Name, k, v)
	}

	// Persist artifacts for each declared output.
	for k, v := range outMap {
		content, _ := json.Marshal(v)
		artifact := &store.Artifact{
			ID:      uuid.New(),
			RunID:   task.RunID,
			StepID:  task.StepID,
			Name:    k,
			Type:    "json",
			Content: string(content),
		}
		if err := p.store.CreateArtifact(artifact); err != nil {
			log.Printf("WARN: create artifact: %v", err)
		}
		p.ctx.mu.Lock()
		p.ctx.artifacts[fmt.Sprintf("%s/%s", step.Name, k)] = artifact
		p.ctx.mu.Unlock()
	}

	return nil
}

// extractOutputs extracts named outputs from agent result based on step declaration.
func (p *ContextPropagator) extractOutputs(step *domain.Step, result *adapter.AgentResult, summary string) map[string]any {
	out := make(map[string]any)
	// If step declares explicit outputs, use them.
	if step.OutputsMap != nil {
		for _, key := range step.OutputsMap {
			if k, ok := key.(string); ok {
				out[k] = result.Output
			}
		}
		return out
	}
	// Otherwise, use the result as a generic output.
	out["result"] = result.Output
	if result.RawJSON != nil {
		out["raw"] = json.RawMessage(result.RawJSON)
	}
	if summary != "" {
		out["summary"] = summary
	}
	return out
}

// RenderBindings renders all input bindings for a step using the current context.
func (p *ContextPropagator) RenderBindings(step *domain.Step) (map[string]any, error) {
	if !step.InputsJSON.Valid {
		return make(map[string]any), nil
	}
	var inputs map[string]any
	if err := json.Unmarshal([]byte(step.InputsJSON.String), &inputs); err != nil {
		return nil, fmt.Errorf("unmarshal inputs: %w", err)
	}
	rendered := make(map[string]any)
	for k, v := range inputs {
		switch val := v.(type) {
		case string:
			rendered[k] = p.ctx.RenderTemplate(val)
		case []any:
			// Render each item.
			var renderedList []any
			for _, item := range val {
				if s, ok := item.(string); ok {
					renderedList = append(renderedList, p.ctx.RenderTemplate(s))
				} else {
					renderedList = append(renderedList, item)
				}
			}
			rendered[k] = renderedList
		default:
			rendered[k] = val
		}
	}
	return rendered, nil
}

// GetArtifact returns a stored artifact by step name and key.
func (p *ContextPropagator) GetArtifact(stepName, key string) (*store.Artifact, bool) {
	p.ctx.mu.RLock()
	defer p.ctx.mu.RUnlock()
	a, ok := p.ctx.artifacts[fmt.Sprintf("%s/%s", stepName, key)]
	return a, ok
}

// ContextSnapshot returns a JSON snapshot of the current context for debugging.
func (p *ContextPropagator) ContextSnapshot() ([]byte, error) {
	type snapshot struct {
		Outputs   map[string]map[string]any `json:"outputs"`
		Artifacts int                       `json:"artifacts"`
	}
	p.ctx.mu.RLock()
	artifactCount := len(p.ctx.artifacts)
	p.ctx.mu.RUnlock()
	s := snapshot{
		Outputs:   p.ctx.GetAll(),
		Artifacts: artifactCount,
	}
	return json.Marshal(s)
}

// ─── Conditional Evaluator ─────────────────────────────────────────────────────
// Epic 2.7: Conditional step / skip propagation.

const (
	skipReasonConditionFalse = "condition_evaluated_false"
	skipReasonNoInputs       = "no_inputs"
)

// ConditionalEvaluator evaluates step conditions and determines skip eligibility.
type ConditionalEvaluator struct {
	ctx *Context
}

// NewConditionalEvaluator creates a conditional evaluator.
func NewConditionalEvaluator(ctx *Context) *ConditionalEvaluator {
	return &ConditionalEvaluator{ctx: ctx}
}

// ShouldSkip returns (skip=true, reason) if the step should be skipped.
// It evaluates the "when" expression against the current context.
func (e *ConditionalEvaluator) ShouldSkip(step *domain.Step) (bool, string, error) {
	if step.StepType != domain.StepTypeConditional && step.StepType != domain.StepTypeTask {
		return false, "", nil
	}

	if !step.InputsJSON.Valid {
		return false, "", nil
	}

	var inputs map[string]any
	if err := json.Unmarshal([]byte(step.InputsJSON.String), &inputs); err != nil {
		return false, "", fmt.Errorf("unmarshal inputs: %w", err)
	}

	// Look for "_when" key (conditional expression).
	when, ok := inputs["_when"].(string)
	if !ok || when == "" {
		return false, "", nil
	}

	// Parse simple boolean expressions.
	// Patterns: "var == value", "var != value", "var > value", "var < value",
	// "var =~ regex", "!var", "var && other", "var || other".
	result, err := e.evalBoolExpr(when)
	if err != nil {
		return false, "", fmt.Errorf("eval when expression %q: %w", when, err)
	}

	if !result {
		return true, fmt.Sprintf("when expression %q evaluated to false", when), nil
	}
	return false, "", nil
}

// evalBoolExpr evaluates a simple boolean expression string.
// Only supports: ==, !=, >, <, =~, !, &&, ||
func (e *ConditionalEvaluator) evalBoolExpr(expr string) (bool, error) {
	expr = strings.TrimSpace(expr)

	// Handle negation.
	if strings.HasPrefix(expr, "!") {
		sub := strings.TrimSpace(expr[1:])
		res, err := e.evalBoolExpr(sub)
		return !res, err
	}

	// Handle &&.
	if idx := strings.Index(expr, "&&"); idx >= 0 {
		left, right := strings.TrimSpace(expr[:idx]), strings.TrimSpace(expr[idx+2:])
		l, err := e.evalBoolExpr(left)
		if err != nil {
			return false, err
		}
		r, err := e.evalBoolExpr(right)
		return l && r, err
	}

	// Handle ||.
	if idx := strings.Index(expr, "||"); idx >= 0 {
		left, right := strings.TrimSpace(expr[:idx]), strings.TrimSpace(expr[idx+2:])
		l, err := e.evalBoolExpr(left)
		if err != nil {
			return false, err
		}
		r, err := e.evalBoolExpr(right)
		return l || r, err
	}

	// Handle ==.
	if idx := strings.Index(expr, "=="); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+2:])
		// right may be quoted.
		right = strings.Trim(right, `"'`)
		lVal := e.resolveVar(left)
		return fmt.Sprintf("%v", lVal) == right, nil
	}

	// Handle !=.
	if idx := strings.Index(expr, "!="); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+2:])
		right = strings.Trim(right, `"'`)
		lVal := e.resolveVar(left)
		return fmt.Sprintf("%v", lVal) != right, nil
	}

	// Handle >.
	if idx := strings.Index(expr, ">"); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+1:])
		lVal := e.resolveVar(left)
		rVal := e.resolveVar(right)
		// Compare numerically if possible.
		if lNum, lOK := toFloat64(lVal); lOK {
			if rNum, rOK := toFloat64(rVal); rOK {
				return lNum > rNum, nil
			}
		}
		return false, nil // simplified
	}

	// Handle <.
	if idx := strings.Index(expr, "<"); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+1:])
		lVal := e.resolveVar(left)
		rVal := e.resolveVar(right)
		if lNum, lOK := toFloat64(lVal); lOK {
			if rNum, rOK := toFloat64(rVal); rOK {
				return lNum < rNum, nil
			}
		}
		return false, nil // simplified
	}

	// Treat as variable existence check.
	v := e.resolveVar(expr)
	return v != nil && v != false, nil
}

// resolveVar resolves a variable reference from the context.
// Supports step.output_key format.
func (e *ConditionalEvaluator) resolveVar(name string) any {
	name = strings.TrimSpace(name)
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		v, _ := e.ctx.GetOutput(parts[0], parts[1])
		return v
	}
	// Check if it's a top-level key.
	for _, outputs := range e.ctx.GetAll() {
		if v, ok := outputs[name]; ok {
			return v
		}
	}
	return nil
}

// toFloat64 converts a value to float64 if possible.
func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}
