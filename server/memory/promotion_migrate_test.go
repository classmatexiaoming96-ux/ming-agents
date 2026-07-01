package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeLegacyMemory writes a pre-Phase-7 memory (no promotion_state) directly.
func writeLegacyMemory(t *testing.T, dir, id, status, layer string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	layerLine := ""
	if layer != "" {
		layerLine = "\nlayer: " + layer
	}
	content := "---\nid: " + id + "\ntype: decision\nproject: ming-agents\ntitle: Legacy memory\nstatus: " + status + layerLine + "\n---\nLegacy body content."
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	return path
}

func TestBackfillPromotionState_DefaultsAndAudit(t *testing.T) {
	vault := useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	activePath := writeLegacyMemory(t, filepath.Join(vault, "notes", "ming-agents"), "leg-active", "active", "l2")
	archivedPath := writeLegacyMemory(t, filepath.Join(vault, "archive", "ming-agents"), "leg-archived", "archived", "l2")

	res, err := BackfillPromotionState(false)
	if err != nil {
		t.Fatalf("BackfillPromotionState error = %v", err)
	}
	if res.Scanned < 2 || res.Updated < 2 {
		t.Fatalf("result = %+v, want scanned>=2 updated>=2", res)
	}

	active, _, err := parseFrontmatterFile(t, activePath)
	if err != nil {
		t.Fatalf("parse active: %v", err)
	}
	if active.PromotionState != PromotionPromoted {
		t.Fatalf("active promotion_state = %q, want promoted", active.PromotionState)
	}
	if active.PromotionAudit != "legacy:no-audit" {
		t.Fatalf("active promotion_audit = %q, want legacy:no-audit", active.PromotionAudit)
	}
	if active.PromotedBy != "" || active.PromotedAt != "" {
		t.Fatalf("migration fabricated promoted_by/promoted_at: %+v", active)
	}

	archived, _, err := parseFrontmatterFile(t, archivedPath)
	if err != nil {
		t.Fatalf("parse archived: %v", err)
	}
	if archived.PromotionState != PromotionArchived {
		t.Fatalf("archived promotion_state = %q, want archived", archived.PromotionState)
	}
	if archived.PromotionAudit != "" {
		t.Fatalf("archived should not get an audit link: %q", archived.PromotionAudit)
	}
}

func TestBackfillPromotionState_Idempotent(t *testing.T) {
	vault := useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	writeLegacyMemory(t, filepath.Join(vault, "notes", "ming-agents"), "leg-active", "active", "l2")

	first, err := BackfillPromotionState(false)
	if err != nil {
		t.Fatalf("first migration error = %v", err)
	}
	if first.Updated != 1 {
		t.Fatalf("first Updated = %d, want 1", first.Updated)
	}
	second, err := BackfillPromotionState(false)
	if err != nil {
		t.Fatalf("second migration error = %v", err)
	}
	if second.Updated != 0 {
		t.Fatalf("second Updated = %d, want 0 (idempotent)", second.Updated)
	}
}

func TestBackfillPromotionState_DryRunDoesNotWrite(t *testing.T) {
	vault := useTempVault(t)
	path := writeLegacyMemory(t, filepath.Join(vault, "notes", "ming-agents"), "leg-active", "active", "l2")
	res, err := BackfillPromotionState(true)
	if err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	if res.Updated != 1 || !res.DryRun {
		t.Fatalf("result = %+v, want dry-run with 1 update planned", res)
	}
	mem, _, err := parseFrontmatterFile(t, path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mem.PromotionState != "" {
		t.Fatalf("dry-run wrote promotion_state: %q", mem.PromotionState)
	}
}

func parseFrontmatterFile(t *testing.T, path string) (Memory, string, error) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return Memory{}, "", err
	}
	return parseFrontmatter(string(raw))
}
