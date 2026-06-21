// Package template provides the Template Registry for WDL template management
// and param-schema validation. This implements Epic 2.10 and Epic 2.14.
package template

import "errors"

// AssertionType represents the type of assertion for a critically node.
type AssertionType string

const (
	// AllCompleted checks that all steps in the template are completed (not skipped).
	AllCompleted AssertionType = "all_completed"
	// NoneSkipped checks that no steps were skipped during the run.
	NoneSkipped AssertionType = "none_skipped"
	// OutputPresent checks that a specific step's output key exists.
	OutputPresent AssertionType = "output_present"
	// ThresholdMet checks that a threshold value was met (from EvaluatedAssertions).
	ThresholdMet AssertionType = "threshold_met"
	// Custom allows custom CUE-like expression evaluation.
	Custom AssertionType = "custom"
)

// CriticallyNode represents a post-run validation check declared at template registration.
// It validates whether the run truly fulfilled the template's promises.
// Epic 2.14: completeness critique node.
type CriticallyNode struct {
	// NodeName is a unique identifier for this critically node within the template.
	NodeName string `json:"node_name"`
	// AssertionType specifies the type of assertion to perform.
	AssertionType AssertionType `json:"assertion_type"`
	// TargetStep is the step name to check. Empty for global checks.
	TargetStep string `json:"target_step,omitempty"`
	// Assertion is a CUE expression or assertion string for custom type.
	// For custom type, this is the expression to evaluate.
	Assertion string `json:"assertion,omitempty"`
	// FailMessage is the message to return if the assertion fails.
	FailMessage string `json:"fail_message,omitempty"`
}

// Validate validates a CriticallyNode configuration.
func (c *CriticallyNode) Validate() error {
	if c.NodeName == "" {
		return errors.New("critically node: node_name cannot be empty")
	}
	switch c.AssertionType {
	case AllCompleted, NoneSkipped:
		// No additional fields required for these types.
		return nil
	case OutputPresent:
		if c.TargetStep == "" {
			return errors.New("critically node: target_step is required for output_present type")
		}
		return nil
	case ThresholdMet:
		if c.Assertion == "" {
			return errors.New("critically node: assertion (threshold expression) is required for threshold_met type")
		}
		return nil
	case Custom:
		if c.Assertion == "" {
			return errors.New("critically node: assertion is required for custom type")
		}
		return nil
	default:
		return errors.New("critically node: unknown assertion_type")
	}
}

// ValidateCriticallyNodes validates all critically nodes in a template.
func ValidateCriticallyNodes(nodes []CriticallyNode) error {
	for _, n := range nodes {
		if err := n.Validate(); err != nil {
			return err
		}
	}
	// Check for duplicate node names.
	names := make(map[string]bool)
	for _, n := range nodes {
		if names[n.NodeName] {
			return errors.New("critically node: duplicate node_name")
		}
		names[n.NodeName] = true
	}
	return nil
}