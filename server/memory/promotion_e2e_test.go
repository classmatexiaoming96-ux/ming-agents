package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPhase7_FullPromotionFlowE2E exercises the whole authority workflow:
// import an AutoMind summary into a candidate, promote it to L2 with audit,
// curate the L2 memory into L1, then revoke the L1 memory. Every state-changing
// step must append an audit event and preserve the source files.
func TestPhase7_FullPromotionFlowE2E(t *testing.T) {
	vault := useTempVault(t)
	day := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fixedNow(t, day)

	// 1. Import an AutoMind summary -> cross-project candidate in the inbox.
	summary := writeSummaryFixture(t, `
run_id: run-e2e-p7
project: ming-agents
source_system: automind
items:
  - kind: cross_project_candidate
    title: Retry review node after transient timeout
    body: Retry once before fallback when the review node times out because reruns succeed.
    tags: [workflow, retry]
    evidence_ref: runs/ming-agents/run-e2e-p7/summary/items.jsonl#sha256=abc
`)
	if _, err := ImportSummary(summary, SummaryImportOptions{Accept: true}); err != nil {
		t.Fatalf("ImportSummary error = %v", err)
	}
	inboxDir := filepath.Join(vault, "notes", "_inbox", "cross_project_candidates")
	candID := onlyMemoryID(t, inboxDir)
	cand, err := loadMemoryByID(candID)
	if err != nil {
		t.Fatalf("load candidate: %v", err)
	}
	if ResolvePromotionState(cand) != PromotionCandidate {
		t.Fatalf("imported candidate state = %q, want candidate", ResolvePromotionState(cand))
	}

	// Back the candidate with three independent frozen runs so it is eligible.
	runs := []string{"run-a", "run-b", "run-c"}
	evidenceRefs := make([]string, 0, len(runs))
	for _, r := range runs {
		makeFrozenRun(t, "ming-agents", r)
		evidenceRefs = append(evidenceRefs, "runs/ming-agents/"+r+"/summary/items.jsonl#sha256=abc")
	}
	cand.SourceRunIDs = runs
	cand.EvidenceRefs = evidenceRefs
	cand.Project = "ming-agents"
	if _, err := writeMemory(cand, inboxDir); err != nil {
		t.Fatalf("update candidate provenance: %v", err)
	}

	// list-pending-promotion should surface the candidate as eligible.
	pending, err := ListPending(PromotionFilter{ToLayer: "l2", ReadyOnly: true})
	if err != nil {
		t.Fatalf("ListPending error = %v", err)
	}
	if len(pending) != 1 || !pending[0].Eligible {
		t.Fatalf("pending = %+v, want one eligible candidate", pending)
	}

	// 2. Promote to L2 -> audit event appended.
	promoteRes, err := Promote(PromotionRequest{
		SourceID: candID, TargetLayer: "l2", Rationale: "three independent frozen runs agree",
		Actor: PromotionActor{Kind: "human", Name: "alice"},
	})
	if err != nil {
		t.Fatalf("Promote error = %v", err)
	}
	l2, err := loadMemoryByID(promoteRes.TargetID)
	if err != nil {
		t.Fatalf("load L2: %v", err)
	}
	if l2.Layer != "l2" || l2.PromotionState != PromotionPromoted {
		t.Fatalf("L2 memory = %+v, want promoted l2", l2)
	}

	// 3. Curate L2 -> L1 -> audit event appended.
	curateRes, err := Curate(CurationRequest{
		SourceID: promoteRes.TargetID, Rationale: "applies to every project",
		Approver: PromotionActor{Kind: "human", Name: "bob"},
	})
	if err != nil {
		t.Fatalf("Curate error = %v", err)
	}
	l1, err := loadMemoryByID(curateRes.TargetID)
	if err != nil {
		t.Fatalf("load L1: %v", err)
	}
	if l1.Layer != "l1" || l1.PromotionState != PromotionPromoted || l1.PromotedBy != "bob" {
		t.Fatalf("L1 memory = %+v, want promoted l1 by bob", l1)
	}

	// 4. Revoke L1 -> archived, source preserved, audit appended.
	if _, err := Revoke(RevokeRequest{
		TargetID: curateRes.TargetID, Reason: "policy retired", Mode: "archive",
		Actor: PromotionActor{Kind: "human", Name: "bob"},
	}); err != nil {
		t.Fatalf("Revoke error = %v", err)
	}
	revoked, err := loadMemoryByID(curateRes.TargetID)
	if err != nil {
		t.Fatalf("load revoked L1: %v", err)
	}
	if revoked.PromotionState != PromotionArchived {
		t.Fatalf("revoked L1 state = %q, want archived", revoked.PromotionState)
	}

	// Audit log must show the full trail (promote, curate, revoke) append-only.
	events, err := ReadPromotionAudit(day)
	if err != nil {
		t.Fatalf("ReadPromotionAudit error = %v", err)
	}
	var promoted, revokedEvents int
	for _, e := range events {
		switch e.EventType {
		case PromotionEventPromoted:
			promoted++
		case PromotionEventRevoked:
			revokedEvents++
		}
	}
	if promoted != 2 || revokedEvents != 1 {
		t.Fatalf("audit events = %+v, want 2 promoted + 1 revoked", events)
	}

	// Source candidate file must be preserved (never deleted).
	if _, err := os.Stat(filepath.Join(inboxDir, candID+".md")); err != nil {
		t.Fatalf("candidate file removed: %v", err)
	}
}

// TestPhase7_L1ConflictSupersedeE2E covers two opposite L1 memories: the second
// is rejected by default and succeeds via supersede mode, replacing the first
// atomically.
func TestPhase7_L1ConflictSupersedeE2E(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))

	writeL2Memory(t, "ming-agents", "l2-yes", "Use connection pooling", "Always use connection pooling for database access.", []string{"db"})
	first, err := Curate(CurationRequest{SourceID: "l2-yes", Rationale: "baseline global rule", Approver: PromotionActor{Kind: "human", Name: "alice"}})
	if err != nil {
		t.Fatalf("seed L1 error = %v", err)
	}

	writeL2Memory(t, "ming-agents", "l2-no", "Use connection pooling", "Do not use connection pooling for database access.", []string{"db"})
	// Default mode: rejected by conflict.
	if _, err := Curate(CurationRequest{SourceID: "l2-no", Rationale: "override", Approver: PromotionActor{Kind: "human", Name: "bob"}}); err == nil {
		t.Fatal("opposite L1 memory must be rejected by default")
	}
	// Supersede mode: succeeds, old L1 becomes superseded.
	second, err := Curate(CurationRequest{
		SourceID: "l2-no", Rationale: "pooling caused leaks in shared services",
		Approver: PromotionActor{Kind: "human", Name: "bob"}, ConflictMode: "supersede",
	})
	if err != nil {
		t.Fatalf("supersede Curate error = %v", err)
	}
	old, err := loadMemoryByID(first.TargetID)
	if err != nil {
		t.Fatalf("load old L1: %v", err)
	}
	if old.PromotionState != PromotionSuperseded || old.SupersededBy != second.TargetID {
		t.Fatalf("old L1 = %+v, want superseded by %s", old, second.TargetID)
	}
	active, err := activeL1Memories()
	if err != nil {
		t.Fatalf("activeL1Memories: %v", err)
	}
	if len(active) != 1 || active[0].ID != second.TargetID {
		t.Fatalf("active L1 = %+v, want only the new memory", active)
	}
}

// onlyMemoryID returns the single memory id found in dir, failing otherwise.
func onlyMemoryID(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			name := e.Name()
			ids = append(ids, name[:len(name)-len(filepath.Ext(name))])
		}
	}
	if len(ids) != 1 {
		t.Fatalf("dir %s has %d files, want 1", dir, len(ids))
	}
	return ids[0]
}
