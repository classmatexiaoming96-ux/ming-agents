package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ming-agents/server/memory"
)

// stubImplicitFeedback captures the ids/log each workflow node feeds into the
// implicit-feedback loop so tests can assert the brief -> output -> score edge
// is actually invoked after an LLM turn.
type implicitCapture struct {
	mu    sync.Mutex
	calls []struct {
		ids []string
		log string
	}
}

func (c *implicitCapture) record(ids []string, log string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, struct {
		ids []string
		log string
	}{ids: ids, log: log})
}

func (c *implicitCapture) sawID(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, call := range c.calls {
		for _, got := range call.ids {
			if got == id {
				return true
			}
		}
	}
	return false
}

func (c *implicitCapture) sawLogContaining(sub string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, call := range c.calls {
		if strings.Contains(call.log, sub) {
			return true
		}
	}
	return false
}

func stubImplicitFeedback(t *testing.T) *implicitCapture {
	t.Helper()
	cap := &implicitCapture{}
	prev := workflowImplicitFeedback
	workflowImplicitFeedback = func(ids []string, log string) ([]memory.ImplicitFeedbackResult, error) {
		cap.record(ids, log)
		return nil, nil
	}
	t.Cleanup(func() { workflowImplicitFeedback = prev })
	return cap
}

func TestClarificationNode_InvokesImplicitFeedback(t *testing.T) {
	restoreBrief := stubWorkflowBrief(t, "remember requirements", memory.BriefAudit{InjectedIDs: []string{"mem_clar"}})
	defer restoreBrief()
	cap := stubImplicitFeedback(t)

	prevRun := runClarificationWithMemoryForNode
	runClarificationWithMemoryForNode = func(ctx context.Context, repoRoot, userInput, memoryBlock string) (string, error) {
		path := filepath.Join(repoRoot, "docs", "requirements-clarity.md")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", err
		}
		return path, os.WriteFile(path, []byte("clarified: connection pooling decision"), 0644)
	}
	defer func() { runClarificationWithMemoryForNode = prevRun }()

	_, err := (&clarificationNode{}).Execute(context.Background(), NodeRequest{
		RunID:    "run-clar-impl",
		RepoRoot: t.TempDir(),
		Spec:     NodeSpec{ID: "clarification", Kind: NodeKindClarification},
		Inputs:   NodeInputs{"input": {Values: map[string]any{"user_input": "build brief injection"}}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !cap.sawID("mem_clar") {
		t.Fatal("clarification node did not feed injected id into implicit feedback")
	}
	if !cap.sawLogContaining("connection pooling") {
		t.Fatal("clarification node did not feed the turn output into implicit feedback")
	}
}

func TestDevelopmentNode_InvokesImplicitFeedback(t *testing.T) {
	restoreBrief := stubWorkflowBrief(t, "dev memory", memory.BriefAudit{InjectedIDs: []string{"mem_dev"}})
	defer restoreBrief()
	cap := stubImplicitFeedback(t)

	prevRun := runDevelopmentOnlyWithMemoryForNode
	runDevelopmentOnlyWithMemoryForNode = func(ctx context.Context, repoRoot string, plan *Plan, memoryBySubtask map[string]string) (*WorkflowState, error) {
		results := []*SubtaskResult{
			{Subtask: plan.Subtasks[0], Output: "implemented the pooling fix"},
		}
		return &WorkflowState{Details: map[string]any{"subtask_results": results}}, nil
	}
	defer func() { runDevelopmentOnlyWithMemoryForNode = prevRun }()

	plan := Plan{Subtasks: []Subtask{{ID: "st1", Description: "add pooling"}}}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal(plan) error = %v", err)
	}
	_, err = (&developmentNode{}).Execute(context.Background(), NodeRequest{
		RunID:    "run-dev-impl",
		RepoRoot: t.TempDir(),
		Spec:     NodeSpec{ID: "development", Kind: NodeKindDevelopment},
		Inputs: NodeInputs{
			"planning": {Values: map[string]any{"plan": json.RawMessage(planBytes)}},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !cap.sawID("mem_dev") {
		t.Fatal("development node did not feed injected id into implicit feedback")
	}
	if !cap.sawLogContaining("pooling fix") {
		t.Fatal("development node did not feed subtask output into implicit feedback")
	}
}

func TestApplyImplicitFeedback_DisabledByEnv(t *testing.T) {
	cap := stubImplicitFeedback(t)
	t.Setenv("WORKFLOW_IMPLICIT_FEEDBACK_DISABLED", "1")
	brief := &BriefInjectResult{Audit: &memory.BriefAudit{InjectedIDs: []string{"mem_x"}}}
	applyImplicitFeedback(brief, "some output")
	if cap.sawID("mem_x") {
		t.Fatal("implicit feedback must be a no-op when disabled by env")
	}
}

func TestApplyImplicitFeedback_NoInjectedIDs(t *testing.T) {
	cap := stubImplicitFeedback(t)
	applyImplicitFeedback(&BriefInjectResult{Audit: &memory.BriefAudit{}}, "output")
	applyImplicitFeedback(nil, "output")
	if len(cap.calls) != 0 {
		t.Fatal("implicit feedback must not fire without injected ids")
	}
}
