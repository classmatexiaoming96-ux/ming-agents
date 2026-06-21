package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// WDL represents a parsed WDL document.
type WDL struct {
	Version  string          `json:"version"`
	Input    json.RawMessage `json:"input,omitempty"`
	Steps    []*Step         `json:"steps"`
	Output   json.RawMessage `json:"output,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// Step represents a WDL step declaration.
type Step struct {
	Name                 string                 `json:"name"`
	StepType             string                 `json:"step_type"` // task|loop|conditional
	Adapter              string                 `json:"adapter"`
	Inputs               map[string]any         `json:"inputs,omitempty"`
	Outputs              []string               `json:"outputs,omitempty"`
	When                 *string                `json:"when,omitempty"` // conditional expression
	MaxIter              int                    `json:"max_iterations,omitempty"`
	Evaluator            string                 `json:"evaluator,omitempty"`
	Threshold            float64                `json:"threshold,omitempty"`
	Body                 json.RawMessage        `json:"body,omitempty"` // loop: step body as JSON
	Retry                int                    `json:"retry,omitempty"`
	Metadata             json.RawMessage        `json:"metadata,omitempty"`
	ConvergenceCondition string                 `json:"convergence_condition,omitempty"` // expression like "score >= 0.9" or "!test.passed"
	ConvergenceVars      []string               `json:"convergence_vars,omitempty"`      // variable names referenced in condition (for tracking)
}

// Validate checks that the WDL is well-formed.
func (w *WDL) Validate() error {
	if len(w.Steps) == 0 {
		return fmt.Errorf("WDL must have at least one step")
	}
	names := make(map[string]bool)
	for _, s := range w.Steps {
		if s.Name == "" {
			return fmt.Errorf("step missing name")
		}
		if names[s.Name] {
			return fmt.Errorf("duplicate step name: %s", s.Name)
		}
		names[s.Name] = true
		if s.StepType == "" {
			s.StepType = "task"
		}
		if s.StepType != "task" && s.StepType != "loop" && s.StepType != "conditional" && s.StepType != "input" {
			return fmt.Errorf("unknown step type: %s", s.StepType)
		}
		// Validate loop step-specific fields
		if s.StepType == "loop" {
			if err := validateLoopStep(s, names); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateLoopStep validates loop-specific constraints.
func validateLoopStep(s *Step, allNames map[string]bool) error {
	// MaxIterations must be > 0
	if s.MaxIter <= 0 {
		return fmt.Errorf("loop step %q: max_iterations must be > 0, got %d", s.Name, s.MaxIter)
	}
	// If ConvergenceCondition is present, it must reference at least one variable
	if s.ConvergenceCondition != "" {
		if len(s.ConvergenceVars) == 0 {
			return fmt.Errorf("loop step %q: convergence_condition %q references no variables (convergence_vars is empty)", s.Name, s.ConvergenceCondition)
		}
		// Verify all ConvergenceVars are valid identifiers (alphanumeric + underscore, not starting with digit)
		for _, v := range s.ConvergenceVars {
			if !isValidIdent(v) {
				return fmt.Errorf("loop step %q: convergence_vars contains invalid identifier %q", s.Name, v)
			}
		}
	}
	return nil
}

// isValidIdent checks if a string is a valid identifier.
// Accepts alphanumeric + underscore + dots (for step.output references like "test.passed").
func isValidIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.') {
				return false
			}
		}
	}
	return true
}

// ResolveInputs resolves input references using context.
// References are in the form "${step_name.output_key}" or "${output_key}".
func (w *WDL) ResolveInputs(inputs map[string]any) error {
	for _, step := range w.Steps {
		if step.Inputs == nil {
			continue
		}
		for k, v := range step.Inputs {
			if s, ok := v.(string); ok {
				step.Inputs[k] = w.resolveRef(s, inputs)
			}
		}
	}
	return nil
}

func (w *WDL) resolveRef(ref string, ctx map[string]any) any {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "${") || !strings.HasSuffix(ref, "}") {
		return ref
	}
	ref = ref[2 : len(ref)-1]
	parts := strings.SplitN(ref, ".", 2)
	if len(parts) != 2 {
		return ref
	}
	stepName, outKey := parts[0], parts[1]
	for _, s := range w.Steps {
		if s.Name == stepName {
			if outputs, ok := ctx[stepName+".outputs"].(map[string]any); ok {
				return outputs[outKey]
			}
		}
	}
	return ref
}

// ToJSONSchema returns a JSON Schema representation of the WDL inputs.
func (w *WDL) ToJSONSchema() (map[string]any, error) {
	schema := map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type":    "object",
		"properties": map[string]any{
			"input": w.Input,
		},
	}
	return schema, nil
}