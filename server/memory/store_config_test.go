package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewStoreUsesInjectedConfig(t *testing.T) {
	originalVault := VaultDir
	originalFTS := ftsDB
	vault := t.TempDir()
	fts := filepath.Join(t.TempDir(), "memory.fts.db")

	ms := NewStore(StoreConfig{VaultDir: vault, FTSDBPath: fts})
	if _, err := ms.Ingest("decision about injected config because tests", "decision", "proj", nil, "manual", ""); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(vault, "notes", "proj")); err != nil {
		t.Fatalf("injected vault was not used: %v", err)
	}
	if VaultDir != originalVault {
		t.Fatalf("global VaultDir changed to %q, want %q", VaultDir, originalVault)
	}
	if ftsDB != originalFTS {
		t.Fatalf("global ftsDB changed to %q, want %q", ftsDB, originalFTS)
	}
}
