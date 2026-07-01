package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCandidate writes an active l2_inbox candidate memory with one evidence
// ref per run id (each ref naming the run bundle that backs it) and returns its
// id. The evidence argument seeds the legacy single EvidenceRef field.
func writeCandidate(t *testing.T, id, project string, runIDs []string, evidence string) string {
	t.Helper()
	evidenceRefs := make([]string, 0, len(runIDs))
	for _, r := range runIDs {
		evidenceRefs = append(evidenceRefs, "runs/"+project+"/"+r+"/summary/items.jsonl#sha256=abc")
	}
	mem := Memory{
		ID:                id,
		Type:              "decision",
		Project:           project,
		Tags:              []string{"workflow"},
		Title:             "Retry review node after transient timeout",
		Body:              "Retry once before fallback when the review node times out.",
		Status:            "active",
		Layer:             "l2_inbox",
		PromotionState:    PromotionCandidate,
		EvidenceRef:       evidence,
		EvidenceRefs:      evidenceRefs,
		SourceRunIDs:      runIDs,
		SourceSystem:      "automind",
		SourceGranularity: "task_summary",
		CreatedAt:         now().Format(dateLayout),
		ExpiresAt:         neverExpires,
	}
	dir := filepath.Join(VaultDir, "notes", "_inbox", "cross_project_candidates")
	if _, err := writeMemory(mem, dir); err != nil {
		t.Fatalf("writeMemory candidate: %v", err)
	}
	return id
}

func TestPromote_RejectsL1Target(t *testing.T) {
	useTempVault(t)
	if _, err := Promote(PromotionRequest{SourceID: "x", TargetLayer: "l1", Rationale: "r"}); err == nil {
		t.Fatal("Promote to l1 must be rejected")
	}
}

func TestPromote_RequiresRationale(t *testing.T) {
	useTempVault(t)
	if _, err := Promote(PromotionRequest{SourceID: "x", TargetLayer: "l2"}); err == nil {
		t.Fatal("Promote without rationale must be rejected")
	}
}

func TestPromote_DryRunDoesNotWriteOrAudit(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		makeFrozenRun(t, "ming-agents", r)
	}
	writeCandidate(t, "cand-1", "ming-agents", runs, "runs/ming-agents/run-a/summary/items.jsonl#sha256=abc")
	res, err := Promote(PromotionRequest{
		SourceID: "cand-1", TargetLayer: "l2", Rationale: "three runs agree",
		Actor: PromotionActor{Kind: "human", Name: "alice"}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Promote dry-run error = %v", err)
	}
	if !res.DryRun || res.AuditEventID != "" {
		t.Fatalf("dry-run result = %+v, want no audit", res)
	}
	if _, err := os.Stat(PromotionAuditDir()); !os.IsNotExist(err) {
		t.Fatalf("audit written in dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(VaultDir, "notes", "ming-agents")); !os.IsNotExist(err) {
		t.Fatalf("L2 note written in dry-run: %v", err)
	}
}

func TestPromote_ApplyWritesL2AndAudit(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		makeFrozenRun(t, "ming-agents", r)
	}
	writeCandidate(t, "cand-1", "ming-agents", runs, "runs/ming-agents/run-a/summary/items.jsonl#sha256=abc")
	res, err := Promote(PromotionRequest{
		SourceID: "cand-1", TargetLayer: "l2", Rationale: "three runs agree",
		Actor: PromotionActor{Kind: "human", Name: "alice"},
	})
	if err != nil {
		t.Fatalf("Promote error = %v", err)
	}
	if res.AuditEventID == "" || res.ToState != PromotionPromoted {
		t.Fatalf("result = %+v, want promoted with audit", res)
	}
	l2Path := filepath.Join(VaultDir, "notes", "ming-agents", res.TargetID+".md")
	raw, err := os.ReadFile(l2Path)
	if err != nil {
		t.Fatalf("read L2: %v", err)
	}
	mem, _, err := parseFrontmatter(string(raw))
	if err != nil {
		t.Fatalf("parse L2: %v", err)
	}
	if mem.Layer != "l2" || mem.PromotionState != PromotionPromoted || mem.PromotedBy != "alice" || mem.PromotionAudit == "" {
		t.Fatalf("L2 metadata = %+v", mem)
	}
	events, err := ReadPromotionAudit(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 || events[0].EventType != PromotionEventPromoted {
		t.Fatalf("audit = %+v err=%v, want one promoted event", events, err)
	}
}

func TestPromote_BlockedBelowThresholdAppendsBlockedAudit(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	makeFrozenRun(t, "ming-agents", "run-a")
	writeCandidate(t, "cand-1", "ming-agents", []string{"run-a"}, "runs/ming-agents/run-a/summary/items.jsonl#sha256=abc")
	_, err := Promote(PromotionRequest{
		SourceID: "cand-1", TargetLayer: "l2", Rationale: "only one run",
		Actor: PromotionActor{Kind: "human", Name: "alice"},
	})
	if err == nil {
		t.Fatal("Promote below threshold without override must be blocked")
	}
	events, _ := ReadPromotionAudit(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	if len(events) != 1 || events[0].EventType != PromotionEventBlocked {
		t.Fatalf("audit = %+v, want one blocked event", events)
	}
}

func TestPromote_SingleRunHumanOverride(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	makeFrozenRun(t, "ming-agents", "run-a")
	writeCandidate(t, "cand-1", "ming-agents", []string{"run-a"}, "runs/ming-agents/run-a/summary/items.jsonl#sha256=abc")
	res, err := Promote(PromotionRequest{
		SourceID: "cand-1", TargetLayer: "l2", Rationale: "high-severity incident with validated fix",
		Actor: PromotionActor{Kind: "human", Name: "alice"}, HumanOverride: true,
	})
	if err != nil {
		t.Fatalf("override Promote error = %v", err)
	}
	if res.ToState != PromotionPromoted {
		t.Fatalf("result = %+v, want promoted via override", res)
	}
}

func TestPromote_RejectsServiceActor(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		makeFrozenRun(t, "ming-agents", r)
	}
	writeCandidate(t, "cand-1", "ming-agents", runs, "")
	_, err := Promote(PromotionRequest{
		SourceID: "cand-1", TargetLayer: "l2", Rationale: "three runs agree",
		Actor: PromotionActor{Kind: "service", Name: "bot"},
	})
	if err == nil {
		t.Fatal("Promote with a service actor must be rejected")
	}
	// A blank human name is also rejected.
	if _, err := Promote(PromotionRequest{
		SourceID: "cand-1", TargetLayer: "l2", Rationale: "three runs agree",
		Actor: PromotionActor{Kind: "human"},
	}); err == nil {
		t.Fatal("Promote with an unnamed human actor must be rejected")
	}
}

func TestPromote_RejectsAlreadyPromotedSource(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	// An active, promoted L2 memory must not be re-promoted through Promote.
	writeL2Memory(t, "ming-agents", "l2-1", "Use pooling", "Pool DB connections.", []string{"db"})
	_, err := Promote(PromotionRequest{
		SourceID: "l2-1", TargetLayer: "l2", Rationale: "again",
		Actor: PromotionActor{Kind: "human", Name: "alice"},
	})
	if err == nil {
		t.Fatal("Promote of an already-promoted L2 source must be rejected")
	}
}

func TestListPending_L2ShowsCandidatesWithVerdict(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		makeFrozenRun(t, "ming-agents", r)
	}
	writeCandidate(t, "cand-eligible", "ming-agents", runs, "runs/ming-agents/run-a/summary/items.jsonl#sha256=abc")
	writeCandidate(t, "cand-blocked", "ming-agents", []string{"run-a"}, "runs/ming-agents/run-a/summary/items.jsonl#sha256=abc")
	pending, err := ListPending(PromotionFilter{ToLayer: "l2"})
	if err != nil {
		t.Fatalf("ListPending error = %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %d, want 2", len(pending))
	}
	byID := map[string]PendingPromotion{}
	for _, p := range pending {
		byID[p.ID] = p
	}
	if !byID["cand-eligible"].Eligible {
		t.Fatalf("cand-eligible should be eligible: %+v", byID["cand-eligible"])
	}
	if byID["cand-blocked"].Eligible {
		t.Fatalf("cand-blocked should not be eligible: %+v", byID["cand-blocked"])
	}
}

func TestRevoke_ArchiveMarksArchivedAppendsAudit(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	writeL2Memory(t, "ming-agents", "l2-1", "Use pooling", "Pool DB connections.", []string{"db"})
	res, err := Revoke(RevokeRequest{TargetID: "l2-1", Reason: "obsolete", Mode: "archive", Actor: PromotionActor{Kind: "human", Name: "alice"}})
	if err != nil {
		t.Fatalf("Revoke error = %v", err)
	}
	if res.ToState != PromotionArchived || res.AuditEventID == "" {
		t.Fatalf("result = %+v, want archived with audit", res)
	}
	m, err := loadMemoryByID("l2-1")
	if err != nil {
		t.Fatalf("load revoked: %v", err)
	}
	if m.Status != "archived" || m.PromotionState != PromotionArchived {
		t.Fatalf("revoked memory = %+v, want archived", m)
	}
}

func TestRevoke_DryRunDoesNotMutate(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	writeL2Memory(t, "ming-agents", "l2-1", "Use pooling", "Pool DB connections.", []string{"db"})
	if _, err := Revoke(RevokeRequest{TargetID: "l2-1", Reason: "obsolete", DryRun: true}); err != nil {
		t.Fatalf("Revoke dry-run error = %v", err)
	}
	m, _ := loadMemoryByID("l2-1")
	if m.Status != "active" {
		t.Fatalf("dry-run mutated memory: %+v", m)
	}
	if _, err := os.Stat(PromotionAuditDir()); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote audit: %v", err)
	}
}

func TestPromotionAudit_AppendOnly(t *testing.T) {
	useTempVault(t)
	day := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fixedNow(t, day)
	if _, err := appendPromotionAudit(PromotionAuditEvent{EventType: PromotionEventReviewStarted, SourceID: "a"}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if _, err := appendPromotionAudit(PromotionAuditEvent{EventType: PromotionEventPromoted, SourceID: "a"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	events, err := ReadPromotionAudit(day)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (append-only)", len(events))
	}
	if events[0].SchemaVersion != promotionAuditSchemaVersion || events[0].EventID == "" {
		t.Fatalf("event missing schema/id: %+v", events[0])
	}
}

func TestPromotionAudit_WritesUnderAuditPromotionPath(t *testing.T) {
	useTempVault(t)
	day := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fixedNow(t, day)
	if _, err := appendPromotionAudit(PromotionAuditEvent{EventType: PromotionEventPromoted, SourceID: "a"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	want := filepath.Join(VaultDir, "audit", "promotion", "2026", "07", "promotion-20260701.jsonl")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("audit not written to %s: %v", want, err)
	}
	// It must NOT be written under the legacy runs namespace.
	if _, err := os.Stat(filepath.Join(VaultDir, "runs", "_promotion_audit")); !os.IsNotExist(err) {
		t.Fatalf("audit written under legacy runs namespace: %v", err)
	}
}

func TestPromotionAudit_ReadsLegacyPath(t *testing.T) {
	useTempVault(t)
	day := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fixedNow(t, day)
	// Simulate a pre-migration log under runs/_promotion_audit.
	legacyDir := filepath.Join(VaultDir, "runs", "_promotion_audit")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacyLine := `{"schema_version":1,"event_id":"evt_legacy","event_type":"promoted","timestamp":"2026-07-01T10:00:00Z","source_id":"old","from_state":"candidate","to_state":"promoted","outcome":"promoted","rationale":"legacy"}`
	if err := os.WriteFile(filepath.Join(legacyDir, "2026-07-01.jsonl"), []byte(legacyLine+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	// A new event goes to the new path.
	if _, err := appendPromotionAudit(PromotionAuditEvent{EventType: PromotionEventRevoked, SourceID: "new"}); err != nil {
		t.Fatalf("append new: %v", err)
	}
	events, err := ReadPromotionAudit(day)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (new + legacy)", len(events))
	}
	var sawLegacy bool
	for _, e := range events {
		if e.EventID == "evt_legacy" {
			sawLegacy = true
		}
	}
	if !sawLegacy {
		t.Fatalf("legacy event not read back: %+v", events)
	}
}
