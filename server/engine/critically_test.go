package engine

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestCriticallyReporter_EvaluateAllCompleted tests the all_completed assertion type.
func TestCriticallyReporter_EvaluateAllCompleted(t *testing.T) {
	reporter := NewCriticallyReporter()

	tests := []struct {
		name       string
		node       CriticallyNode
		runRecord  RunRecord
		wantPassed bool
	}{
		{
			name: "all completed - no skipped steps",
			node: CriticallyNode{
				NodeName:      "check_all_done",
				AssertionType: AllCompleted,
			},
			runRecord: RunRecord{
				RunID:        uuid.New(),
				SkippedSteps: []SkippedStep{},
				RunStatus:    "completed",
			},
			wantPassed: true,
		},
		{
			name: "all completed - has skipped steps",
			node: CriticallyNode{
				NodeName:      "check_all_done",
				AssertionType: AllCompleted,
			},
			runRecord: RunRecord{
				RunID: uuid.New(),
				SkippedSteps: []SkippedStep{
					{StepName: "fix", Reason: "when condition false"},
				},
				RunStatus: "completed",
			},
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, msg := reporter.EvaluateRun(tt.node, tt.runRecord)
			if got != tt.wantPassed {
				t.Errorf("EvaluateRun() passed = %v, want %v, msg = %s", got, tt.wantPassed, msg)
			}
		})
	}
}

// TestCriticallyReporter_EvaluateNoneSkipped tests the none_skipped assertion type.
func TestCriticallyReporter_EvaluateNoneSkipped(t *testing.T) {
	reporter := NewCriticallyReporter()

	tests := []struct {
		name       string
		node       CriticallyNode
		runRecord  RunRecord
		wantPassed bool
	}{
		{
			name: "none skipped - no skipped steps",
			node: CriticallyNode{
				NodeName:      "check_no_skip",
				AssertionType: NoneSkipped,
			},
			runRecord: RunRecord{
				RunID:        uuid.New(),
				SkippedSteps: []SkippedStep{},
				RunStatus:    "completed",
			},
			wantPassed: true,
		},
		{
			name: "none skipped - has skipped steps",
			node: CriticallyNode{
				NodeName:      "check_no_skip",
				AssertionType: NoneSkipped,
			},
			runRecord: RunRecord{
				RunID: uuid.New(),
				SkippedSteps: []SkippedStep{
					{StepName: "test", Reason: "when condition false"},
				},
				RunStatus: "completed",
			},
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, msg := reporter.EvaluateRun(tt.node, tt.runRecord)
			if got != tt.wantPassed {
				t.Errorf("EvaluateRun() passed = %v, want %v, msg = %s", got, tt.wantPassed, msg)
			}
		})
	}
}

// TestCriticallyReporter_EvaluateThresholdMet tests the threshold_met assertion type.
func TestCriticallyReporter_EvaluateThresholdMet(t *testing.T) {
	reporter := NewCriticallyReporter()

	tests := []struct {
		name       string
		node       CriticallyNode
		runRecord  RunRecord
		wantPassed bool
	}{
		{
			name: "threshold met - found and passed",
			node: CriticallyNode{
				NodeName:      "check_score",
				AssertionType: ThresholdMet,
				Assertion:     "test_score",
			},
			runRecord: RunRecord{
				RunID:        uuid.New(),
				RunStatus:    "completed",
				EvaluatedAssertions: []AssertionResult{
					{StepName: "test", Assertion: "test_score", Passed: true, ActualValue: 0.85},
				},
			},
			wantPassed: true,
		},
		{
			name: "threshold met - found but failed",
			node: CriticallyNode{
				NodeName:      "check_score",
				AssertionType: ThresholdMet,
				Assertion:     "test_score",
			},
			runRecord: RunRecord{
				RunID:        uuid.New(),
				RunStatus:    "completed",
				EvaluatedAssertions: []AssertionResult{
					{StepName: "test", Assertion: "test_score", Passed: false, ActualValue: 0.3},
				},
			},
			wantPassed: false,
		},
		{
			name: "threshold met - not found",
			node: CriticallyNode{
				NodeName:      "check_score",
				AssertionType: ThresholdMet,
				Assertion:     "nonexistent",
			},
			runRecord: RunRecord{
				RunID:        uuid.New(),
				RunStatus:    "completed",
				EvaluatedAssertions: []AssertionResult{
					{StepName: "test", Assertion: "test_score", Passed: true, ActualValue: 0.85},
				},
			},
			wantPassed: false,
		},
		{
			name: "threshold met - found in effective thresholds",
			node: CriticallyNode{
				NodeName:      "check_threshold",
				AssertionType: ThresholdMet,
				Assertion:     "max_iter",
			},
			runRecord: RunRecord{
				RunID:        uuid.New(),
				RunStatus:    "completed",
				EffectiveThresholds: map[string]float64{
					"max_iter": 5.0,
				},
			},
			wantPassed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, msg := reporter.EvaluateRun(tt.node, tt.runRecord)
			if got != tt.wantPassed {
				t.Errorf("EvaluateRun() passed = %v, want %v, msg = %s", got, tt.wantPassed, msg)
			}
		})
	}
}

// TestCriticallyReporter_EvaluateCustom tests the custom assertion type.
func TestCriticallyReporter_EvaluateCustom(t *testing.T) {
	reporter := NewCriticallyReporter()

	tests := []struct {
		name       string
		node       CriticallyNode
		runRecord  RunRecord
		wantPassed bool
	}{
		{
			name: "custom - run_status == completed",
			node: CriticallyNode{
				NodeName:      "check_status",
				AssertionType: Custom,
				Assertion:     "run_status == completed",
			},
			runRecord: RunRecord{
				RunID:     uuid.New(),
				RunStatus: "completed",
			},
			wantPassed: true,
		},
		{
			name: "custom - run_status == failed",
			node: CriticallyNode{
				NodeName:      "check_status",
				AssertionType: Custom,
				Assertion:     "run_status == completed",
			},
			runRecord: RunRecord{
				RunID:     uuid.New(),
				RunStatus: "failed",
			},
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, msg := reporter.EvaluateRun(tt.node, tt.runRecord)
			if got != tt.wantPassed {
				t.Errorf("EvaluateRun() passed = %v, want %v, msg = %s", got, tt.wantPassed, msg)
			}
		})
	}
}

// TestCriticallyReporter_EvaluateAll tests EvaluateAll with multiple nodes.
func TestCriticallyReporter_EvaluateAll(t *testing.T) {
	reporter := NewCriticallyReporter()

	nodes := []CriticallyNode{
		{NodeName: "check1", AssertionType: NoneSkipped},
		{NodeName: "check2", AssertionType: ThresholdMet, Assertion: "score"},
	}

	runRecord := RunRecord{
		RunID:     uuid.New(),
		RunStatus: "completed",
		SkippedSteps: []SkippedStep{
			{StepName: "fix", Reason: "when false"},
		},
		EvaluatedAssertions: []AssertionResult{
			{StepName: "test", Assertion: "score", Passed: true, ActualValue: 0.9},
		},
	}

	results := reporter.EvaluateAll(nodes, runRecord)

	if len(results) != 2 {
		t.Errorf("EvaluateAll() returned %d results, want 2", len(results))
	}

	// First node should fail (skipped steps exist).
	if results[0].NodeName != "check1" || results[0].Passed {
		t.Errorf("results[0] = %+v, want NodeName=check1 Passed=false", results[0])
	}

	// Second node should pass.
	if results[1].NodeName != "check2" || !results[1].Passed {
		t.Errorf("results[1] = %+v, want NodeName=check2 Passed=true", results[1])
	}
}

// TestCriticallyResult_EvaluatedAtTimestamp tests that the timestamp is properly set.
func TestCriticallyResult_EvaluatedAtTimestamp(t *testing.T) {
	reporter := NewCriticallyReporter()

	nodes := []CriticallyNode{
		{NodeName: "check1", AssertionType: NoneSkipped},
	}

	runRecord := RunRecord{
		RunID:        uuid.New(),
		RunStatus:    "completed",
		SkippedSteps: []SkippedStep{},
	}

	results := reporter.EvaluateAll(nodes, runRecord)

	if len(results) != 1 {
		t.Fatal("expected 1 result")
	}

	if results[0].EvaluatedAt == "" {
		t.Error("EvaluatedAt should not be empty")
	}

	// Verify it's a valid timestamp format.
	if _, err := time.Parse(time.RFC3339, results[0].EvaluatedAt); err != nil {
		t.Errorf("EvaluatedAt is not a valid RFC3339 timestamp: %v", err)
	}
}