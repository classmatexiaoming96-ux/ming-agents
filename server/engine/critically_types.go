package engine

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
// Note: This is an engine-local copy of the template.CriticallyNode to avoid
// circular dependencies between engine and template packages.
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

// CriticallyResult represents the result of evaluating a critically node.
// Epic 2.14: completeness critique node.
type CriticallyResult struct {
	NodeName    string `json:"node_name"`
	Passed      bool   `json:"passed"`
	Message     string `json:"message"`
	EvaluatedAt string `json:"evaluated_at"`
}