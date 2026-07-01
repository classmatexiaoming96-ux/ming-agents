package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ming-agents/server/memory"
)

func TestRunBundleReceiver_FreezeOnRunEnd(t *testing.T) {
	oldVault := memory.VaultDir
	memory.VaultDir = t.TempDir()
	t.Cleanup(func() { memory.VaultDir = oldVault })

	registry := NewNodeRegistry()
	registry.Register(NodeKindClarification, func() WorkflowNode {
		return staticNode{kind: NodeKindClarification}
	})
	executor := NewNodeExecutor(registry, NodeServices{})
	spec := WorkflowSpec{
		RunID: "run-freeze",
		Nodes: []NodeSpec{{
			ID:   "clarification",
			Kind: NodeKindClarification,
		}},
	}

	repoRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("MkdirAll repoRoot error = %v", err)
	}
	if _, err := executor.Run(context.Background(), repoRoot, spec, nil); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	root := memory.RunBundlePath("repo", "run-freeze")
	if _, err := os.Stat(filepath.Join(root, "_frozen")); err != nil {
		t.Fatalf("_frozen missing: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("run bundle root missing: %v", err)
	}
	if got := info.Mode().Perm(); got != 0555 {
		t.Fatalf("run bundle root mode = %v, want 0555", got)
	}
}

type staticNode struct {
	kind NodeKind
}

func (n staticNode) Kind() NodeKind { return n.kind }

func (n staticNode) Execute(ctx context.Context, req NodeRequest) (*NodeResult, error) {
	return &NodeResult{NodeID: req.Spec.ID, Status: NodeStatusCompleted}, nil
}
