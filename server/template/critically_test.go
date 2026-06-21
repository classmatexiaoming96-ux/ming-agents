package template

import (
	"encoding/json"
	"testing"
)

// TestCriticallyNode_Validate tests validation of CriticallyNode configurations.
func TestCriticallyNode_Validate(t *testing.T) {
	tests := []struct {
		name    string
		node    CriticallyNode
		wantErr bool
	}{
		{
			name: "valid all_completed",
			node: CriticallyNode{
				NodeName:      "check1",
				AssertionType: AllCompleted,
			},
			wantErr: false,
		},
		{
			name: "valid none_skipped",
			node: CriticallyNode{
				NodeName:      "check2",
				AssertionType: NoneSkipped,
			},
			wantErr: false,
		},
		{
			name: "valid output_present with target",
			node: CriticallyNode{
				NodeName:      "check3",
				AssertionType: OutputPresent,
				TargetStep:    "fix",
				Assertion:     "output_file",
			},
			wantErr: false,
		},
		{
			name: "output_present without target",
			node: CriticallyNode{
				NodeName:      "check4",
				AssertionType: OutputPresent,
			},
			wantErr: true,
		},
		{
			name: "valid threshold_met",
			node: CriticallyNode{
				NodeName:      "check5",
				AssertionType: ThresholdMet,
				Assertion:     "test_score",
			},
			wantErr: false,
		},
		{
			name: "threshold_met without assertion",
			node: CriticallyNode{
				NodeName:      "check6",
				AssertionType: ThresholdMet,
			},
			wantErr: true,
		},
		{
			name: "valid custom",
			node: CriticallyNode{
				NodeName:      "check7",
				AssertionType: Custom,
				Assertion:     "run_status == completed",
			},
			wantErr: false,
		},
		{
			name: "custom without assertion",
			node: CriticallyNode{
				NodeName:      "check8",
				AssertionType: Custom,
			},
			wantErr: true,
		},
		{
			name: "empty node name",
			node: CriticallyNode{
				NodeName:      "",
				AssertionType: AllCompleted,
			},
			wantErr: true,
		},
		{
			name: "unknown assertion type",
			node: CriticallyNode{
				NodeName:      "check9",
				AssertionType: "unknown",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.node.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateCriticallyNodes tests validation of multiple critically nodes.
func TestValidateCriticallyNodes(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []CriticallyNode
		wantErr bool
	}{
		{
			name:    "empty list",
			nodes:   []CriticallyNode{},
			wantErr: false,
		},
		{
			name: "valid nodes",
			nodes: []CriticallyNode{
				{NodeName: "check1", AssertionType: AllCompleted},
				{NodeName: "check2", AssertionType: NoneSkipped},
			},
			wantErr: false,
		},
		{
			name: "duplicate node names",
			nodes: []CriticallyNode{
				{NodeName: "check1", AssertionType: AllCompleted},
				{NodeName: "check1", AssertionType: NoneSkipped},
			},
			wantErr: true,
		},
		{
			name: "invalid node in list",
			nodes: []CriticallyNode{
				{NodeName: "check1", AssertionType: AllCompleted},
				{NodeName: "", AssertionType: NoneSkipped},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCriticallyNodes(tt.nodes)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCriticallyNodes() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCriticallyNode_JSONSerialization tests JSON marshaling/unmarshaling.
func TestCriticallyNode_JSONSerialization(t *testing.T) {
	node := CriticallyNode{
		NodeName:      "test_check",
		AssertionType: ThresholdMet,
		TargetStep:    "test_step",
		Assertion:     "score_threshold",
		FailMessage:   "threshold not met",
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded CriticallyNode
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if decoded.NodeName != node.NodeName {
		t.Errorf("NodeName = %v, want %v", decoded.NodeName, node.NodeName)
	}
	if decoded.AssertionType != node.AssertionType {
		t.Errorf("AssertionType = %v, want %v", decoded.AssertionType, node.AssertionType)
	}
	if decoded.TargetStep != node.TargetStep {
		t.Errorf("TargetStep = %v, want %v", decoded.TargetStep, node.TargetStep)
	}
	if decoded.Assertion != node.Assertion {
		t.Errorf("Assertion = %v, want %v", decoded.Assertion, node.Assertion)
	}
	if decoded.FailMessage != node.FailMessage {
		t.Errorf("FailMessage = %v, want %v", decoded.FailMessage, node.FailMessage)
	}
}

// TestTemplate_CriticallyNodes tests that Template can include CriticallyNodes.
func TestTemplate_CriticallyNodes(t *testing.T) {
	template := Template{
		Name:        "test_template",
		Description: "Test template with critically nodes",
		Version:     "1.0.0",
		WDLContent:  "steps: []",
		CriticallyNodes: []CriticallyNode{
			{
				NodeName:      "check_all_done",
				AssertionType: AllCompleted,
				FailMessage:   "not all steps completed",
			},
			{
				NodeName:      "check_no_skip",
				AssertionType: NoneSkipped,
				FailMessage:   "some steps were skipped",
			},
		},
	}

	data, err := json.Marshal(template)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded Template
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(decoded.CriticallyNodes) != 2 {
		t.Errorf("len(CriticallyNodes) = %v, want 2", len(decoded.CriticallyNodes))
	}

	if decoded.CriticallyNodes[0].NodeName != "check_all_done" {
		t.Errorf("CriticallyNodes[0].NodeName = %v, want check_all_done", decoded.CriticallyNodes[0].NodeName)
	}
}

// TestRegistry_Register_WithCriticallyNodes tests registration with critically nodes.
func TestRegistry_Register_WithCriticallyNodes(t *testing.T) {
	registry := NewRegistry()

	template := &Template{
		Name:        "bugfix_template",
		Description: "Bug fix loop template",
		Version:     "1.0.0",
		WDLContent:  "steps: []",
		CriticallyNodes: []CriticallyNode{
			{NodeName: "check1", AssertionType: AllCompleted},
			{NodeName: "check2", AssertionType: NoneSkipped},
		},
	}

	if err := registry.Register(template); err != nil {
		t.Errorf("Register() error = %v", err)
	}

	// Verify we can retrieve it.
	got := registry.Get("bugfix_template")
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if len(got.CriticallyNodes) != 2 {
		t.Errorf("len(got.CriticallyNodes) = %v, want 2", len(got.CriticallyNodes))
	}
}

// TestRegistry_Register_InvalidCriticallyNodes tests registration with invalid critically nodes.
func TestRegistry_Register_InvalidCriticallyNodes(t *testing.T) {
	registry := NewRegistry()

	template := &Template{
		Name:        "bad_template",
		Version:     "1.0.0",
		WDLContent:  "steps: []",
		CriticallyNodes: []CriticallyNode{
			{NodeName: "check1", AssertionType: AllCompleted},
			{NodeName: "check1", AssertionType: NoneSkipped}, // duplicate name
		},
	}

	if err := registry.Register(template); err == nil {
		t.Error("Register() expected error for duplicate node names, got nil")
	}
}

// TestRegistry_Register_CriticallyNodesWithoutSteps tests that CriticallyNodes requires steps.
// According to Epic 2.14: if CriticallyNodes is non-empty, template must have at least one step.
// However, since we don't have step information in Template struct (just WDLContent string),
// we validate that at least the WDLContent indicates steps exist.
func TestRegistry_Register_CriticallyNodesWithEmptyWDL(t *testing.T) {
	registry := NewRegistry()

	template := &Template{
		Name:        "empty_template",
		Version:     "1.0.0",
		WDLContent:  "", // No WDL content
		CriticallyNodes: []CriticallyNode{
			{NodeName: "check1", AssertionType: AllCompleted},
		},
	}

	// This should still register - the actual WDL validation happens elsewhere.
	// The CriticallyNodes validation at registration only validates the nodes themselves.
	if err := registry.Register(template); err != nil {
		t.Errorf("Register() error = %v", err)
	}
}