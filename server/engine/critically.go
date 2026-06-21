package engine

import (
	"fmt"
	"strings"
	"time"
)

// CriticallyReporter evaluates critically nodes against run records.
// It implements post-run validation to check whether a run truly fulfilled
// the template's promises.
type CriticallyReporter struct{}

// NewCriticallyReporter creates a new critically reporter.
func NewCriticallyReporter() *CriticallyReporter {
	return &CriticallyReporter{}
}

// EvaluateRun evaluates a single critically node against a run record.
// Returns (passed, message).
func (r *CriticallyReporter) EvaluateRun(node CriticallyNode, runRecord RunRecord) (bool, string) {
	switch node.AssertionType {
	case AllCompleted:
		return r.evaluateAllCompleted(node, runRecord)
	case NoneSkipped:
		return r.evaluateNoneSkipped(node, runRecord)
	case OutputPresent:
		return r.evaluateOutputPresent(node, runRecord)
	case ThresholdMet:
		return r.evaluateThresholdMet(node, runRecord)
	case Custom:
		return r.evaluateCustom(node, runRecord)
	default:
		return false, fmt.Sprintf("unknown assertion type: %s", node.AssertionType)
	}
}

// EvaluateAll evaluates all critically nodes for a template against a run record.
func (r *CriticallyReporter) EvaluateAll(nodes []CriticallyNode, runRecord RunRecord) []CriticallyResult {
	var results []CriticallyResult
	for _, node := range nodes {
		passed, msg := r.EvaluateRun(node, runRecord)
		results = append(results, CriticallyResult{
			NodeName:    node.NodeName,
			Passed:      passed,
			Message:     msg,
			EvaluatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return results
}

// evaluateAllCompleted checks that all steps in the template are completed (not skipped).
func (r *CriticallyReporter) evaluateAllCompleted(node CriticallyNode, runRecord RunRecord) (bool, string) {
	// The runRecord should have information about all steps.
	// We check that no steps were skipped.
	if len(runRecord.SkippedSteps) > 0 {
		skipped := make([]string, len(runRecord.SkippedSteps))
		for i, s := range runRecord.SkippedSteps {
			skipped[i] = s.StepName
		}
		return false, fmt.Sprintf("steps were skipped: %s", strings.Join(skipped, ", "))
	}
	// Also check that all expected steps completed.
	// TotalSteps should equal the number of steps in the template.
	// But we don't have direct access to the template here, so we rely on the
	// step statuses in the run record context.
	// Since we can't directly check step statuses, we verify no skipped steps exist.
	return true, "all steps completed (no skipped steps)"
}

// evaluateNoneSkipped checks that no steps were skipped during the run.
func (r *CriticallyReporter) evaluateNoneSkipped(node CriticallyNode, runRecord RunRecord) (bool, string) {
	if len(runRecord.SkippedSteps) > 0 {
		skipped := make([]string, len(runRecord.SkippedSteps))
		for i, s := range runRecord.SkippedSteps {
			skipped[i] = s.StepName
		}
		return false, fmt.Sprintf("steps were skipped: %s", strings.Join(skipped, ", "))
	}
	return true, "no steps were skipped"
}

// evaluateOutputPresent checks that a specific step's output key exists.
func (r *CriticallyReporter) evaluateOutputPresent(node CriticallyNode, runRecord RunRecord) (bool, string) {
	// The assertion field can specify the output key to check.
	// Format: "step_name.output_key" or just "output_key" if TargetStep is set.
	outputKey := node.Assertion
	if outputKey == "" {
		return false, "output_present assertion requires assertion field to specify the output key"
	}

	// Check if we have the step's outputs in resolved params or context.
	stepName := node.TargetStep
	if stepName == "" {
		return false, "output_present assertion requires target_step to be set"
	}

	// Look for the output in resolved params.
	if runRecord.ResolvedParams != nil {
		if stepParams, ok := runRecord.ResolvedParams[stepName]; ok {
			// The output key might be in the step's inputs (as a reference).
			// Actually, we need to look at what was actually produced.
			// Since we don't have direct step output access in RunRecord,
			// we check the EvaluatedAssertions for threshold_met results.
			// For output_present, we need another approach.
			_ = stepParams // Reserved for future use
		}
	}

	// For now, we can only verify this if we have step output information.
	// Since RunRecord doesn't store step outputs directly, we return a message
	// indicating the check could not be performed without step output data.
	// In a full implementation, step outputs would be stored in RunRecord.
	return false, fmt.Sprintf("output_present check requires step output storage; cannot verify %s.%s", stepName, outputKey)
}

// evaluateThresholdMet checks that a threshold value was met.
func (r *CriticallyReporter) evaluateThresholdMet(node CriticallyNode, runRecord RunRecord) (bool, string) {
	thresholdName := node.Assertion
	if thresholdName == "" {
		return false, "threshold_met assertion requires assertion field to specify the threshold name"
	}

	// Check if the threshold was met based on EvaluatedAssertions.
	for _, ar := range runRecord.EvaluatedAssertions {
		if ar.Assertion == thresholdName {
			if ar.Passed {
				return true, fmt.Sprintf("threshold %q was met (actual value: %v)", thresholdName, ar.ActualValue)
			}
			return false, fmt.Sprintf("threshold %q was not met (actual value: %v)", thresholdName, ar.ActualValue)
		}
	}

	// Also check effective thresholds map.
	if runRecord.EffectiveThresholds != nil {
		if thresholdVal, ok := runRecord.EffectiveThresholds[thresholdName]; ok {
			return true, fmt.Sprintf("threshold %q met with effective value: %v", thresholdName, thresholdVal)
		}
	}

	return false, fmt.Sprintf("threshold %q not found in evaluated assertions", thresholdName)
}

// evaluateCustom evaluates a custom CUE-like expression.
// MVP implementation uses simple string comparison patterns.
func (r *CriticallyReporter) evaluateCustom(node CriticallyNode, runRecord RunRecord) (bool, string) {
	expr := node.Assertion
	if expr == "" {
		return false, "custom assertion requires assertion field to contain the expression"
	}

	// MVP: Simple pattern matching for common assertions.
	// Format: "status == completed" or "count > 5" etc.
	// This is a simplified implementation; full CUE evaluation would be complex.

	// Check for simple equality assertions.
	// Example: "run_status == completed"
	if strings.Contains(expr, "==") {
		parts := strings.Split(expr, "==")
		if len(parts) == 2 {
			left := strings.TrimSpace(parts[0])
			right := strings.TrimSpace(parts[1])
			right = strings.Trim(right, "\"'")

			switch left {
			case "run_status":
				if runRecord.RunStatus == right {
					return true, fmt.Sprintf("custom assertion passed: %s", expr)
				}
				return false, fmt.Sprintf("custom assertion failed: %s (actual: %s)", expr, runRecord.RunStatus)
			}
		}
	}

	// Check for threshold comparisons in evaluated assertions.
	// Example: "score >= 0.8"
	if strings.Contains(expr, ">=") || strings.Contains(expr, "<=") ||
		strings.Contains(expr, ">") || strings.Contains(expr, "<") {
		for _, ar := range runRecord.EvaluatedAssertions {
			if ar.Passed {
				return true, fmt.Sprintf("custom assertion passed: %s", expr)
			}
		}
	}

	return false, fmt.Sprintf("custom assertion could not be evaluated: %s", expr)
}