package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckReuseAckAtBlocksUntilAccepted(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-reuse"
	phase := "planning"

	passed, err := CheckReuseAckAt(context.Background(), repoRoot, runID, phase)
	if err != nil {
		t.Fatalf("CheckReuseAckAt() missing ack error = %v", err)
	}
	if passed {
		t.Fatal("CheckReuseAckAt() missing ack passed, want blocked")
	}

	ackPath := filepath.Join(repoRoot, ".workflow", "runs", runID, "reuse-ack.json")
	if err := os.MkdirAll(filepath.Dir(ackPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeJSONAtomic(ackPath, ReuseAck{
		RunID:     runID,
		Phase:     phase,
		Timestamp: time.Now(),
		Accepted:  false,
	}); err != nil {
		t.Fatalf("write rejected ack: %v", err)
	}

	passed, err = CheckReuseAckAt(context.Background(), repoRoot, runID, phase)
	if err != nil {
		t.Fatalf("CheckReuseAckAt() rejected ack error = %v", err)
	}
	if passed {
		t.Fatal("CheckReuseAckAt() rejected ack passed, want blocked")
	}

	if err := writeJSONAtomic(ackPath, ReuseAck{
		RunID:     runID,
		Phase:     phase,
		Timestamp: time.Now(),
		Applied: []ReuseHit{{
			MemoryID: "mem_1",
			Title:    "Known fix",
			Score:    4.2,
			WhyUsed:  "Matches this phase",
		}},
		Accepted: true,
	}); err != nil {
		t.Fatalf("write accepted ack: %v", err)
	}

	passed, err = CheckReuseAckAt(context.Background(), repoRoot, runID, phase)
	if err != nil {
		t.Fatalf("CheckReuseAckAt() accepted ack error = %v", err)
	}
	if !passed {
		t.Fatal("CheckReuseAckAt() accepted ack blocked, want passed")
	}
}

func TestWriteReuseAckAtRoundTrip(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "test-run-001"
	ack := ReuseAck{
		Accepted: true,
		Applied:  []ReuseHit{{MemoryID: "mem_x", Score: 4.5}},
		Note:     "round trip test",
	}
	if err := WriteReuseAckAt(context.Background(), repoRoot, runID, "clarification", ack); err != nil {
		t.Fatalf("WriteReuseAckAt: %v", err)
	}

	accepted, err := CheckReuseAckAt(context.Background(), repoRoot, runID, "clarification")
	if err != nil {
		t.Fatalf("CheckReuseAckAt: %v", err)
	}
	if !accepted {
		t.Fatalf("expected accepted=true after write")
	}
}

func TestPlanningNodeMissingReuseAckDoesNotBlock(t *testing.T) {
	node := &planningNode{}
	result, err := node.Execute(context.Background(), NodeRequest{
		RunID:    "test-run-001",
		RepoRoot: t.TempDir(),
		Spec:     NodeSpec{ID: "node2", Kind: NodeKindPlanning},
		Inputs: NodeInputs{
			"clarification": {
				Outputs: map[string]string{"clarification_output": "missing-clarification.md"},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected missing clarification file error")
	}
	if result == nil {
		t.Fatalf("expected result")
	}
	if result.Status == NodeStatusBlocked {
		t.Fatalf("expected missing reuse ack not to block planning")
	}
}

func TestRenderReuseMarkdownIncludesHits(t *testing.T) {
	content := renderReuseMarkdown("run-reuse", "clarification", []ReuseHit{
		{MemoryID: "mem_1", Title: "Use existing approval gate", Score: 4.5, WhyUsed: "same workflow gate"},
	})

	for _, want := range []string{
		"# Reuse for clarification",
		"run_id: run-reuse",
		"mem_1",
		"Use existing approval gate",
		"same workflow gate",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("renderReuseMarkdown() missing %q in:\n%s", want, content)
		}
	}
}
