package workflow

import (
	"encoding/json"
	"testing"
)

func TestStepJSONParsing(t *testing.T) {
	// Test parsing a loop step with all fields including ConvergenceCondition
	jsonData := `{
		"name": "fix-loop",
		"step_type": "loop",
		"adapter": "agent",
		"max_iterations": 6,
		"evaluator": "agent",
		"threshold": 0.9,
		"convergence_condition": "score >= 0.9",
		"convergence_vars": ["score", "test.passed"],
		"body": [{"name": "fix", "step_type": "task", "adapter": "claude-code"}],
		"inputs": {
			"max_iterations": 6,
			"evaluator": "agent",
			"convergence_condition": "score >= 0.9",
			"convergence_vars": ["score", "test.passed"]
		}
	}`

	var step Step
	if err := json.Unmarshal([]byte(jsonData), &step); err != nil {
		t.Fatalf("failed to unmarshal step: %v", err)
	}

	if step.Name != "fix-loop" {
		t.Errorf("expected name 'fix-loop', got %q", step.Name)
	}
	if step.StepType != "loop" {
		t.Errorf("expected step_type 'loop', got %q", step.StepType)
	}
	if step.MaxIter != 6 {
		t.Errorf("expected max_iterations 6, got %d", step.MaxIter)
	}
	if step.Evaluator != "agent" {
		t.Errorf("expected evaluator 'agent', got %q", step.Evaluator)
	}
	if step.Threshold != 0.9 {
		t.Errorf("expected threshold 0.9, got %f", step.Threshold)
	}
	if step.ConvergenceCondition != "score >= 0.9" {
		t.Errorf("expected convergence_condition 'score >= 0.9', got %q", step.ConvergenceCondition)
	}
	if len(step.ConvergenceVars) != 2 {
		t.Errorf("expected 2 convergence_vars, got %d", len(step.ConvergenceVars))
	}
	if step.ConvergenceVars[0] != "score" || step.ConvergenceVars[1] != "test.passed" {
		t.Errorf("unexpected convergence_vars: %v", step.ConvergenceVars)
	}
}

func TestWDLValidateLoopStep(t *testing.T) {
	tests := []struct {
		name    string
		wdl     WDL
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid loop step",
			wdl: WDL{
				Version: "1.0",
				Steps: []*Step{
					{
						Name:                 "fix-loop",
						StepType:             "loop",
						MaxIter:              6,
						Evaluator:            "agent",
						ConvergenceCondition: "score >= 0.9",
						ConvergenceVars:      []string{"score"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "loop step with zero max_iterations",
			wdl: WDL{
				Version: "1.0",
				Steps: []*Step{
					{
						Name:                 "fix-loop",
						StepType:             "loop",
						MaxIter:              0,
						Evaluator:            "agent",
						ConvergenceCondition: "score >= 0.9",
						ConvergenceVars:      []string{"score"},
					},
				},
			},
			wantErr: true,
			errMsg:  "max_iterations must be > 0",
		},
		{
			name: "loop step with negative max_iterations",
			wdl: WDL{
				Version: "1.0",
				Steps: []*Step{
					{
						Name:                 "fix-loop",
						StepType:             "loop",
						MaxIter:              -1,
						Evaluator:            "agent",
						ConvergenceCondition: "score >= 0.9",
						ConvergenceVars:      []string{"score"},
					},
				},
			},
			wantErr: true,
			errMsg:  "max_iterations must be > 0",
		},
		{
			name: "loop step with condition but no vars",
			wdl: WDL{
				Version: "1.0",
				Steps: []*Step{
					{
						Name:                 "fix-loop",
						StepType:             "loop",
						MaxIter:              6,
						Evaluator:            "agent",
						ConvergenceCondition: "score >= 0.9",
						ConvergenceVars:      []string{},
					},
				},
			},
			wantErr: true,
			errMsg:  "references no variables",
		},
		{
			name: "loop step with invalid convergence_var identifier",
			wdl: WDL{
				Version: "1.0",
				Steps: []*Step{
					{
						Name:                 "fix-loop",
						StepType:             "loop",
						MaxIter:              6,
						Evaluator:            "agent",
						ConvergenceCondition: "score >= 0.9",
						ConvergenceVars:      []string{"123invalid"},
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid identifier",
		},
		{
			name: "loop step with valid identifiers",
			wdl: WDL{
				Version: "1.0",
				Steps: []*Step{
					{
						Name:                 "fix-loop",
						StepType:             "loop",
						MaxIter:              6,
						Evaluator:            "agent",
						ConvergenceCondition: "!test.passed",
						ConvergenceVars:      []string{"test", "test.passed", "_under", "CamelCase", "with_123"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "loop step with boolean negation condition",
			wdl: WDL{
				Version: "1.0",
				Steps: []*Step{
					{
						Name:                 "fix-loop",
						StepType:             "loop",
						MaxIter:              10,
						Evaluator:            "no_progress",
						ConvergenceCondition: "!test.passed",
						ConvergenceVars:      []string{"test", "test.passed"},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.wdl.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
				} else if !contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestIsValidIdent(t *testing.T) {
	valid := []string{
		"x", "X", "_", "foo", "Bar", "foo_bar",
		"abc123", "ABC123", "_under", "camelCase",
		"test.passed", "with_123", "CamelCase",
	}
	invalid := []string{
		"", "123", "1abc", "foo-bar", "foo bar", "foo@bar",
	}

	for _, s := range valid {
		if !isValidIdent(s) {
			t.Errorf("isValidIdent(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isValidIdent(s) {
			t.Errorf("isValidIdent(%q) = true, want false", s)
		}
	}
}

func TestWDLWithMixedSteps(t *testing.T) {
	wdl := WDL{
		Version: "1.0",
		Steps: []*Step{
			{
				Name:     "input",
				StepType: "input",
			},
			{
				Name:     "locate",
				StepType: "task",
			},
			{
				Name:                 "fix-loop",
				StepType:             "loop",
				MaxIter:              6,
				Evaluator:            "agent",
				ConvergenceCondition: "!test.passed",
				ConvergenceVars:      []string{"test", "test.passed"},
			},
			{
				Name:     "pr",
				StepType: "task",
			},
		},
	}

	if err := wdl.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}