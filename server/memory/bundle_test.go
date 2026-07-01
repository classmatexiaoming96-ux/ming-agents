package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBundlePath_IsolatedFromArchive(t *testing.T) {
	oldVault := VaultDir
	VaultDir = t.TempDir()
	t.Cleanup(func() { VaultDir = oldVault })

	got := RunBundlePath("ming-agents", "run-123")
	want := filepath.Join(VaultDir, "runs", "ming-agents", "run-123")
	if got != want {
		t.Fatalf("RunBundlePath() = %q, want %q", got, want)
	}
	if strings.Contains(got, filepath.Join("archive", "ming-agents")) {
		t.Fatalf("RunBundlePath() mixed with archive namespace: %s", got)
	}
}

func TestRunBundleReceiver_ImmutableAfterFreeze(t *testing.T) {
	oldVault := VaultDir
	VaultDir = t.TempDir()
	t.Cleanup(func() { VaultDir = oldVault })

	receiver := NewRunBundleReceiver("ming-agents", "run-immutable")
	if err := receiver.ReceivePhaseReuse("planning", "memory hits"); err != nil {
		t.Fatalf("ReceivePhaseReuse before freeze error = %v", err)
	}
	if err := receiver.Freeze(); err != nil {
		t.Fatalf("Freeze error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(receiver.Root(), "_frozen")); err != nil {
		t.Fatalf("_frozen marker missing: %v", err)
	}
	manifestPath := filepath.Join(receiver.Root(), "manifest.json")
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if got := info.Mode().Perm(); got != 0444 {
		t.Fatalf("manifest mode = %v, want 0444", got)
	}
	if err := receiver.ReceivePhaseReuse("review", "late write"); err == nil {
		t.Fatal("ReceivePhaseReuse after freeze error = nil, want immutable error")
	}
}
