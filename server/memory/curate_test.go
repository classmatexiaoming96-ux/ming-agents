package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeL2Memory writes an active, promoted L2 project memory and returns its id.
func writeL2Memory(t *testing.T, project, id, title, body string, tags []string) string {
	t.Helper()
	mem := Memory{
		ID:             id,
		Type:           "decision",
		Project:        project,
		Tags:           tags,
		Title:          title,
		Body:           body,
		Score:          5,
		Status:         "active",
		Layer:          "l2",
		PromotionState: PromotionPromoted,
		CreatedAt:      now().Format(dateLayout),
		ExpiresAt:      neverExpires,
	}
	if _, err := writeMemory(mem, filepath.Join(VaultDir, "notes", project)); err != nil {
		t.Fatalf("writeMemory: %v", err)
	}
	return id
}

func TestCurate_RequiresHumanApprover(t *testing.T) {
	useTempVault(t)
	writeL2Memory(t, "ming-agents", "l2-1", "Use connection pooling", "Pool DB connections to reuse handles.", []string{"db"})
	_, err := Curate(CurationRequest{SourceID: "l2-1", Rationale: "global", Approver: PromotionActor{Kind: "service", Name: "bot"}})
	if err == nil {
		t.Fatal("Curate without human approver must be rejected")
	}
	_, err = Curate(CurationRequest{SourceID: "l2-1", Rationale: "global", Approver: PromotionActor{Kind: "human"}})
	if err == nil {
		t.Fatal("Curate without approver name must be rejected")
	}
}

func TestCurate_RequiresRationale(t *testing.T) {
	useTempVault(t)
	writeL2Memory(t, "ming-agents", "l2-1", "Use connection pooling", "Pool DB connections to reuse handles.", []string{"db"})
	_, err := Curate(CurationRequest{SourceID: "l2-1", Approver: PromotionActor{Kind: "human", Name: "alice"}})
	if err == nil {
		t.Fatal("Curate without rationale must be rejected")
	}
}

func TestCurate_DryRunDoesNotWriteOrAudit(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	writeL2Memory(t, "ming-agents", "l2-1", "Use connection pooling", "Pool DB connections to reuse handles.", []string{"db"})
	res, err := Curate(CurationRequest{
		SourceID:  "l2-1",
		Rationale: "applies to all projects",
		Approver:  PromotionActor{Kind: "human", Name: "alice"},
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("Curate dry-run error = %v", err)
	}
	if !res.DryRun || res.AuditEventID != "" {
		t.Fatalf("dry-run result = %+v, want no audit event", res)
	}
	if _, err := os.Stat(L1NotesPath()); !os.IsNotExist(err) {
		t.Fatalf("L1 notes written during dry-run: %v", err)
	}
	if _, err := os.Stat(PromotionAuditDir()); !os.IsNotExist(err) {
		t.Fatalf("audit written during dry-run: %v", err)
	}
}

func TestCurate_ApplyWritesL1AndAudit(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	writeL2Memory(t, "ming-agents", "l2-1", "Use connection pooling", "Pool DB connections to reuse handles.", []string{"db"})
	res, err := Curate(CurationRequest{
		SourceID:  "l2-1",
		Rationale: "applies to all projects",
		Approver:  PromotionActor{Kind: "human", Name: "alice"},
	})
	if err != nil {
		t.Fatalf("Curate error = %v", err)
	}
	if res.AuditEventID == "" || res.ToState != PromotionPromoted {
		t.Fatalf("result = %+v, want promoted with audit id", res)
	}
	l1Path := filepath.Join(L1NotesPath(), res.TargetID+".md")
	raw, err := os.ReadFile(l1Path)
	if err != nil {
		t.Fatalf("read L1 memory: %v", err)
	}
	mem, _, err := parseFrontmatter(string(raw))
	if err != nil {
		t.Fatalf("parse L1 memory: %v", err)
	}
	if mem.Layer != "l1" || mem.PromotionState != PromotionPromoted || mem.PromotedBy != "alice" {
		t.Fatalf("L1 memory metadata = %+v", mem)
	}
	if mem.PromotionAudit == "" {
		t.Fatalf("L1 memory missing audit reference")
	}
	events, err := ReadPromotionAudit(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ReadPromotionAudit error = %v", err)
	}
	if len(events) != 1 || events[0].EventType != PromotionEventPromoted {
		t.Fatalf("audit events = %+v, want one promoted event", events)
	}
}

func TestCurate_BlocksOnContradictionByDefault(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	// Existing L1: use connection pooling. New L2: do NOT use connection pooling.
	writeL2Memory(t, "ming-agents", "l2-yes", "Use connection pooling", "Always use connection pooling for database access.", []string{"db"})
	if _, err := Curate(CurationRequest{SourceID: "l2-yes", Rationale: "baseline", Approver: PromotionActor{Kind: "human", Name: "alice"}}); err != nil {
		t.Fatalf("seed L1 error = %v", err)
	}
	writeL2Memory(t, "ming-agents", "l2-no", "Use connection pooling", "Do not use connection pooling for database access.", []string{"db"})
	_, err := Curate(CurationRequest{SourceID: "l2-no", Rationale: "override", Approver: PromotionActor{Kind: "human", Name: "bob"}})
	if err == nil {
		t.Fatal("contradicting L1 promotion must be blocked by default")
	}
}

func TestCurate_SupersedeModeAtomicallyReplaces(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	writeL2Memory(t, "ming-agents", "l2-yes", "Use connection pooling", "Always use connection pooling for database access.", []string{"db"})
	first, err := Curate(CurationRequest{SourceID: "l2-yes", Rationale: "baseline", Approver: PromotionActor{Kind: "human", Name: "alice"}})
	if err != nil {
		t.Fatalf("seed L1 error = %v", err)
	}
	writeL2Memory(t, "ming-agents", "l2-no", "Use connection pooling", "Do not use connection pooling for database access.", []string{"db"})
	second, err := Curate(CurationRequest{
		SourceID:     "l2-no",
		Rationale:    "pooling caused leaks in shared services",
		Approver:     PromotionActor{Kind: "human", Name: "bob"},
		ConflictMode: "supersede",
	})
	if err != nil {
		t.Fatalf("supersede Curate error = %v", err)
	}
	// Old L1 must now be superseded.
	old, err := loadMemoryByID(first.TargetID)
	if err != nil {
		t.Fatalf("load old L1: %v", err)
	}
	if old.Status != "superseded" || old.SupersededBy != second.TargetID {
		t.Fatalf("old L1 = %+v, want superseded by %s", old, second.TargetID)
	}
	// New L1 records what it supersedes.
	active, err := activeL1Memories()
	if err != nil {
		t.Fatalf("activeL1Memories: %v", err)
	}
	if len(active) != 1 || active[0].ID != second.TargetID {
		t.Fatalf("active L1 = %+v, want only %s", active, second.TargetID)
	}
}

func TestCurate_RejectsNonPromotedSources(t *testing.T) {
	cases := []struct {
		name  string
		layer string
		state PromotionState
		dir   string
	}{
		{"l2_inbox candidate", "l2_inbox", PromotionCandidate, filepath.Join("notes", "_inbox", "cross_project_candidates")},
		{"l2 under_review", "l2", PromotionUnderReview, filepath.Join("notes", "ming-agents")},
		{"l2 rejected", "l2", PromotionRejected, filepath.Join("notes", "ming-agents")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempVault(t)
			fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
			mem := Memory{
				ID:             "src-1",
				Type:           "decision",
				Project:        "ming-agents",
				Tags:           []string{"db"},
				Title:          "Use connection pooling",
				Body:           "Pool DB connections to reuse handles.",
				Score:          5,
				Status:         "active",
				Layer:          tc.layer,
				PromotionState: tc.state,
				CreatedAt:      now().Format(dateLayout),
				ExpiresAt:      neverExpires,
			}
			if _, err := writeMemory(mem, filepath.Join(VaultDir, tc.dir)); err != nil {
				t.Fatalf("writeMemory: %v", err)
			}
			_, err := Curate(CurationRequest{SourceID: "src-1", Rationale: "global", Approver: PromotionActor{Kind: "human", Name: "alice"}})
			if err == nil {
				t.Fatalf("Curate must reject non-promoted L2 source (%s)", tc.name)
			}
		})
	}
}

func TestCurate_RejectsArchivedSource(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	mem := Memory{
		ID:        "src-arch",
		Type:      "decision",
		Project:   "ming-agents",
		Tags:      []string{"db"},
		Title:     "Use connection pooling",
		Body:      "Pool DB connections to reuse handles.",
		Score:     5,
		Status:    "archived",
		Layer:     "l2",
		CreatedAt: now().Format(dateLayout),
		ExpiresAt: neverExpires,
	}
	if _, err := writeMemory(mem, filepath.Join(VaultDir, "archive", "ming-agents")); err != nil {
		t.Fatalf("writeMemory: %v", err)
	}
	if _, err := Curate(CurationRequest{SourceID: "src-arch", Rationale: "global", Approver: PromotionActor{Kind: "human", Name: "alice"}}); err == nil {
		t.Fatal("Curate must reject an archived L2 source")
	}
}

func TestDetectL1Conflicts_DuplicateNotBlocking(t *testing.T) {
	existing := []Memory{{
		ID: "l1-a", Layer: "l1", Status: "active", PromotionState: PromotionPromoted,
		Title: "Use connection pooling", Body: "Always use connection pooling for database access.", Tags: []string{"db"},
	}}
	cand := Memory{Title: "Use connection pooling", Body: "Always use connection pooling for database access.", Tags: []string{"db"}}
	report := DetectL1Conflicts(cand, existing)
	if report.HasBlockingConflict {
		t.Fatalf("duplicate must not block: %+v", report)
	}
	if len(report.PossibleDuplicates) != 1 {
		t.Fatalf("PossibleDuplicates = %v, want one", report.PossibleDuplicates)
	}
}
