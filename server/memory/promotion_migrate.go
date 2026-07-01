package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrationResult reports what BackfillPromotionState changed.
type MigrationResult struct {
	Scanned int
	Updated int
	DryRun  bool
}

// BackfillPromotionState writes the read-time promotion default into any memory
// frontmatter that lacks an explicit promotion_state. Read-time defaulting
// (ResolvePromotionState) already covers correctness at runtime; this migration
// makes the on-disk state explicit for auditability.
//
// It is non-destructive and idempotent:
//   - a memory that already has a recognised promotion_state is left untouched;
//   - promoted memories that lack a promotion_audit link get "legacy:no-audit";
//   - promoted_by and promoted_at are never fabricated (unknown audit is better
//     than false audit);
//   - bodies and all other metadata are preserved.
//
// Dry-run reports the counts without writing.
func BackfillPromotionState(dryRun bool) (MigrationResult, error) {
	result := MigrationResult{DryRun: dryRun}
	for _, sub := range []string{"notes", "archive", "inbox"} {
		root := filepath.Join(VaultDir, sub)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			mem, body, err := parseFrontmatter(string(raw))
			if err != nil {
				return err
			}
			if mem.ID == "" {
				return nil
			}
			result.Scanned++

			changed := false
			if !IsValidPromotionState(mem.PromotionState) {
				mem.PromotionState = ResolvePromotionState(mem)
				changed = true
			}
			// Only promoted memories carry an audit link; legacy ones have none.
			if mem.PromotionState == PromotionPromoted && mem.PromotionAudit == "" {
				mem.PromotionAudit = "legacy:no-audit"
				changed = true
			}
			if !changed {
				return nil
			}
			result.Updated++
			if dryRun {
				return nil
			}
			mem.Body = body
			if _, err := writeMemory(mem, filepath.Dir(path)); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	return result, nil
}
