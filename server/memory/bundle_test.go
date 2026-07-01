package memory

import (
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
