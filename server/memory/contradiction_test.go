package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBigramNoveltyCJK is the regression for the word-set→char-bigram switch:
// the old `\w+` word-set matched nothing on pure-CJK text, so novelty stayed
// 1.0 and duplicate Chinese notes were always accepted as "novel". char-bigram
// works at the rune level, so a near-duplicate Chinese note now collapses
// novelty and lowers the score.
func TestBigramNoveltyCJK(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	content := "数据库连接池因为泄漏而被耗尽，需要在处理函数里释放连接"
	first := mustIngest(t, content, "incident", "p", []string{"db"}, "manual")

	// Same Chinese content again: char-bigram sets are identical → similarity 1
	// → novelty ~0 → strictly lower score. (Under the old word-set Jaccard this
	// assertion failed: both scored identically because novelty stayed 1.0.)
	second := mustIngest(t, content, "incident", "p", []string{"db"}, "manual")
	if second.Score >= first.Score {
		t.Errorf("CJK duplicate score %g not < original %g (novelty did not drop)", second.Score, first.Score)
	}

	// And the raw primitive: identical CJK strings → Jaccard 1, disjoint → 0.
	if got := bigramJaccard("数据库连接池", "数据库连接池"); got != 1.0 {
		t.Errorf("bigramJaccard(identical CJK) = %g, want 1.0", got)
	}
	if got := bigramJaccard("数据库连接池", "完全不同的主题内容"); got != 0.0 {
		t.Errorf("bigramJaccard(disjoint CJK) = %g, want 0.0", got)
	}
}

// placeMemory writes an active memory with controlled fields into notes/{project}.
func placeMemory(t *testing.T, m Memory) Memory {
	t.Helper()
	if m.Status == "" {
		m.Status = "active"
	}
	if m.CreatedAt == "" {
		m.CreatedAt = now().Format(dateLayout)
	}
	project := m.Project
	if project == "" {
		project = "p"
		m.Project = project
	}
	dir := filepath.Join(VaultDir, "notes", project)
	path, err := writeMemory(m, dir)
	if err != nil {
		t.Fatalf("placeMemory(%s): %v", m.ID, err)
	}
	m.Path = path
	return m
}

func auditLogExists(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(VaultDir, contradictionsLog))
	return err == nil
}

func TestResolveContradictionsDryRun(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	// Winner clearly outranks loser on Score (composite tier, margin 2.0 ≥ 1.0).
	winner := placeMemory(t, Memory{ID: "mem_winner1", Type: "decision", Project: "p", Score: 4.0, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Type: "decision", Project: "p", Score: 2.0, Body: "do not use pooling"})

	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Similarity: 0.8, Source: "manual"}}

	// DryRun: decision is computed and returned, but nothing is written.
	res, err := ResolveContradictions(cands, ResolveOptions{DryRun: true, AutoEvict: true})
	if err != nil {
		t.Fatalf("DryRun ResolveContradictions: %v", err)
	}
	if len(res) != 1 || res[0].Action != "superseded" {
		t.Fatalf("DryRun result = %+v, want one superseded", res)
	}
	if res[0].Winner != winner.ID || res[0].Loser != loser.ID {
		t.Errorf("DryRun winner/loser = %s/%s, want %s/%s", res[0].Winner, res[0].Loser, winner.ID, loser.ID)
	}
	// No side effects: both still active, no audit log.
	active, _ := readAllMemories("active", "")
	if len(active) != 2 {
		t.Errorf("DryRun mutated vault: %d active memories, want 2", len(active))
	}
	if auditLogExists(t) {
		t.Errorf("DryRun wrote %s, must write nothing", contradictionsLog)
	}

	// Real run: loser is superseded in place (Revoke rewrites the file) and the
	// winner gains supersedes.
	res, err = ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: PromotionActor{Kind: "human", Name: "alice"}})
	if err != nil {
		t.Fatalf("real ResolveContradictions: %v", err)
	}
	if res[0].Action != "superseded" {
		t.Fatalf("real action = %q, want superseded", res[0].Action)
	}
	if res[0].PromotionAuditEventID == "" {
		t.Errorf("superseded result missing promotion audit event id")
	}
	sup, _ := readAllMemories("superseded", "")
	if len(sup) != 1 || sup[0].ID != loser.ID {
		t.Fatalf("expected loser superseded, got %+v", sup)
	}
	if sup[0].SupersededBy != winner.ID || sup[0].SupersededAt == "" || sup[0].SupersededReason == "" {
		t.Errorf("superseded metadata incomplete: %+v", sup[0])
	}
	if sup[0].PromotionState != PromotionSuperseded {
		t.Errorf("loser promotion state = %q, want superseded", sup[0].PromotionState)
	}
	act, _ := readAllMemories("active", "")
	if len(act) != 1 || act[0].ID != winner.ID {
		t.Fatalf("expected only winner active, got %+v", act)
	}
	if len(act[0].Supersedes) != 1 || act[0].Supersedes[0] != loser.ID {
		t.Errorf("winner.supersedes = %v, want [%s]", act[0].Supersedes, loser.ID)
	}
	if !auditLogExists(t) {
		t.Errorf("real run did not write %s", contradictionsLog)
	}

	// Unsupersede reverses it: loser back to active, winner's supersedes cleared.
	restored, err := Unsupersede(loser.ID, "detector false positive", PromotionActor{Kind: "human", Name: "alice"})
	if err != nil {
		t.Fatalf("Unsupersede: %v", err)
	}
	if restored.Status != "active" || restored.SupersededBy != "" {
		t.Errorf("restored memory not clean: %+v", restored)
	}
	if restored.PromotionState != PromotionPromoted {
		t.Errorf("restored promotion state = %q, want promoted", restored.PromotionState)
	}
	act, _ = readAllMemories("active", "")
	if len(act) != 2 {
		t.Errorf("after unsupersede: %d active, want 2", len(act))
	}
	for _, m := range act {
		if m.ID == winner.ID && len(m.Supersedes) != 0 {
			t.Errorf("winner.supersedes not cleared: %v", m.Supersedes)
		}
	}
}

// TestResolveAbstainsOnLowConfidence: a weak signal must never evict.
func TestResolveAbstainsOnLowConfidence(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	a := placeMemory(t, Memory{ID: "mem_aaaaaa01", Project: "p", Score: 4.0, Body: "x"})
	b := placeMemory(t, Memory{ID: "mem_bbbbbb01", Project: "p", Score: 1.0, Body: "y"})

	// Confidence below resolveConfidence → flagged, never superseded.
	cands := []Contradiction{{A: a.ID, B: b.ID, Confidence: 0.3, Source: "lexical"}}
	res, err := ResolveContradictions(cands, ResolveOptions{AutoEvict: true})
	if err != nil {
		t.Fatalf("ResolveContradictions: %v", err)
	}
	if res[0].Action != "flagged" {
		t.Errorf("low-confidence action = %q, want flagged", res[0].Action)
	}
	if sup, _ := readAllMemories("superseded", ""); len(sup) != 0 {
		t.Errorf("low-confidence pass superseded %d memories, want 0", len(sup))
	}
	// Both sides carry the durable conflicts_with marker.
	act, _ := readAllMemories("active", "")
	for _, m := range act {
		if len(m.ConflictsWith) == 0 {
			t.Errorf("memory %s missing conflicts_with marker after flag", m.ID)
		}
	}
}

// TestResolveExplicitTrumps: the side with explicit feedback wins regardless of
// a small score gap.
func TestResolveExplicitTrumps(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	// b has a slightly higher raw score but a has explicit feedback.
	a := placeMemory(t, Memory{ID: "mem_explicit", Project: "p", Score: 3.0, ExplicitHits: 2, Body: "x"})
	b := placeMemory(t, Memory{ID: "mem_noexplic", Project: "p", Score: 3.2, Body: "y"})

	cands := []Contradiction{{A: a.ID, B: b.ID, Confidence: 0.9, Source: "manual"}}
	res, err := ResolveContradictions(cands, ResolveOptions{AutoEvict: true})
	if err != nil {
		t.Fatalf("ResolveContradictions: %v", err)
	}
	if res[0].Action != "superseded" || res[0].Winner != a.ID || res[0].Loser != b.ID {
		t.Errorf("explicit-trumps result = %+v, want a wins", res[0])
	}
	if !strings.Contains(res[0].Reason, "explicit-trumps") {
		t.Errorf("reason = %q, want explicit-trumps", res[0].Reason)
	}
}

// TestResolveExplicitTrumpsAbstainsOnBlowout: explicit feedback grants a bounded
// edge, not an unconditional win. A stale, low-score memory with a single
// explicit hit must NOT evict a far-superior fresh memory — when the explicit
// winner trails by more than evictMargin, Layer 2 abstains (flags) instead of
// superseding.
func TestResolveExplicitTrumpsAbstainsOnBlowout(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	// stale: score 1.0, one explicit hit, ~2 years old → survivorScore ≈ 1.5.
	// fresh: score 4.5, no explicit feedback, brand new → survivorScore ≈ 5.5.
	stale := placeMemory(t, Memory{ID: "mem_stale001", Project: "p", Score: 1.0, ExplicitHits: 1, CreatedAt: "2024-06-08", Body: "x"})
	fresh := placeMemory(t, Memory{ID: "mem_fresh001", Project: "p", Score: 4.5, Body: "y"})

	cands := []Contradiction{{A: stale.ID, B: fresh.ID, Confidence: 0.9, Source: "manual"}}
	res, err := ResolveContradictions(cands, ResolveOptions{AutoEvict: true})
	if err != nil {
		t.Fatalf("ResolveContradictions: %v", err)
	}
	if res[0].Action != "flagged" {
		t.Errorf("blowout action = %q, want flagged (explicit winner too inferior to evict)", res[0].Action)
	}
	if !strings.Contains(res[0].Reason, "explicit-trumps abstained") {
		t.Errorf("reason = %q, want explicit-trumps abstained", res[0].Reason)
	}
	if sup, _ := readAllMemories("superseded", ""); len(sup) != 0 {
		t.Errorf("blowout superseded %d memories, want 0 (the superior memory must survive)", len(sup))
	}
}

// TestRecencyFactorUnparseable: a missing/corrupt CreatedAt yields the neutral
// 0.5 prior, not 0 — a parsing artifact must not strip a full wRecency unit.
func TestRecencyFactorUnparseable(t *testing.T) {
	for _, bad := range []string{"", "not-a-date", "2024/06/08"} {
		if got := recencyFactor(Memory{CreatedAt: bad}); got != 0.5 {
			t.Errorf("recencyFactor(%q) = %g, want 0.5", bad, got)
		}
	}
}

// TestResolveDedupAndAutoEvictOff verifies PairKey dedup (highest confidence
// wins) and that AutoEvict=false downgrades an eviction to flag-only.
func TestResolveDedupAndAutoEvictOff(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	a := placeMemory(t, Memory{ID: "mem_aa", Project: "p", Score: 4.0, Body: "x"})
	b := placeMemory(t, Memory{ID: "mem_bb", Project: "p", Score: 2.0, Body: "y"})

	// Two candidates for the same pair (order swapped) → must dedup to one.
	cands := []Contradiction{
		{A: a.ID, B: b.ID, Confidence: 0.65, Source: "lexical"},
		{A: b.ID, B: a.ID, Confidence: 0.95, Source: "manual"},
	}
	res, err := ResolveContradictions(cands, ResolveOptions{AutoEvict: false})
	if err != nil {
		t.Fatalf("ResolveContradictions: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("dedup failed: %d results, want 1", len(res))
	}
	// AutoEvict off → flagged, not superseded; nothing moved to archive.
	if res[0].Action != "flagged" {
		t.Errorf("AutoEvict=false action = %q, want flagged", res[0].Action)
	}
	if sup, _ := readAllMemories("superseded", ""); len(sup) != 0 {
		t.Errorf("AutoEvict=false superseded %d, want 0", len(sup))
	}
}

// TestScanGroupSkipsOtherProject: a polarity-flip pair in *different* projects is
// not a contradiction — the partition must never pair across projects.
func TestScanGroupSkipsOtherProject(t *testing.T) {
	a := Memory{ID: "mem_aaa", Project: "alpha", Status: "active", Body: "use connection pooling for the database"}
	b := Memory{ID: "mem_bbb", Project: "beta", Status: "active", Body: "do not use connection pooling for the database"}

	got := lexicalContradictionScan([]Memory{a, b})
	if len(got) != 0 {
		t.Errorf("cross-project pair flagged: got %d contradictions, want 0 (%+v)", len(got), got)
	}
}

// TestScanGroupSameProjectDetectsContradiction: the identical bodies, now in the
// same project, ARE flagged — partitioning must not suppress real contradictions.
func TestScanGroupSameProjectDetectsContradiction(t *testing.T) {
	a := Memory{ID: "mem_aaa", Project: "alpha", Status: "active", Body: "use connection pooling for the database"}
	b := Memory{ID: "mem_bbb", Project: "alpha", Status: "active", Body: "do not use connection pooling for the database"}

	got := lexicalContradictionScan([]Memory{a, b})
	if len(got) != 1 {
		t.Fatalf("same-project contradiction not detected: got %d, want 1 (%+v)", len(got), got)
	}
	if got[0].A != "mem_aaa" || got[0].B != "mem_bbb" { // canonical A<B
		t.Errorf("pair = %s|%s, want mem_aaa|mem_bbb", got[0].A, got[0].B)
	}
	if got[0].Source != "lexical" {
		t.Errorf("source = %q, want lexical", got[0].Source)
	}

	// Inactive members are excluded from the scan regardless of project.
	b.Status = "archived"
	if got := lexicalContradictionScan([]Memory{a, b}); len(got) != 0 {
		t.Errorf("inactive member paired: got %d, want 0 (%+v)", len(got), got)
	}
}

// TestBigramPrecomputeConsistency: the precomputed-set path (jaccardOfSets over
// charBigrams) must equal the raw-string bigramJaccard for every input pair,
// including empty and single-rune edge cases. This guards the scanGroup refactor
// against silently diverging from Ingest's novelty math.
func TestBigramPrecomputeConsistency(t *testing.T) {
	samples := []string{
		"use connection pooling for the database",
		"do not use connection pooling for the database",
		"数据库连接池因为泄漏而被耗尽",
		"完全不同的主题内容",
		"avoid global mutable state",
		"x",
		"",
	}
	for i := range samples {
		for j := range samples {
			want := bigramJaccard(samples[i], samples[j])
			got := jaccardOfSets(charBigrams(samples[i]), charBigrams(samples[j]))
			if got != want {
				t.Errorf("jaccardOfSets(%q,%q) = %g, bigramJaccard = %g", samples[i], samples[j], got, want)
			}
		}
	}
}

// TestCleanupDrivesResolutionPhase verifies the planted call chain
// Cleanup() → resolutionPhase() → ResolveContradictions actually runs: a
// same-project polarity-flip pair is picked up by the at-rest detector, funneled
// through the resolver, flagged (AutoEvict is off, so flag-only), and recorded in
// the audit log — all without panicking and without evicting anything.
func TestCleanupDrivesResolutionPhase(t *testing.T) {
	vault := useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	// Two contradictory, never-expiring memories in the same project (empty
	// ExpiresAt → Cleanup's archival pass leaves them active). Equal scores keep
	// the composite margin under evictMargin, so the decision is flagged either
	// way — the point is that resolution runs, not which tier fires.
	a := placeMemory(t, Memory{ID: "mem_pool_yes", Project: "p", Score: 3.0, Body: "use connection pooling for the database"})
	b := placeMemory(t, Memory{ID: "mem_pool_no0", Project: "p", Score: 3.0, Body: "do not use connection pooling for the database"})

	res, err := Cleanup()
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Flag-only: nothing superseded, nothing left active circulation.
	if res.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0 (AutoEvict off → flag-only)", res.Resolved)
	}
	if sup, _ := readAllMemories("superseded", ""); len(sup) != 0 {
		t.Errorf("Cleanup superseded %d memories, want 0", len(sup))
	}
	active, _ := readAllMemories("active", "")
	if len(active) != 2 {
		t.Fatalf("active count = %d, want 2 (both survive flag-only resolution)", len(active))
	}

	// Resolution ran: the audit log exists and recorded a flagged decision for the
	// pair, and both memories now carry the durable conflicts_with marker.
	if !auditLogExists(t) {
		t.Fatal("Cleanup did not write the contradiction audit log → resolutionPhase never reached ResolveContradictions")
	}
	logRaw, err := os.ReadFile(filepath.Join(vault, contradictionsLog))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	logStr := string(logRaw)
	if !strings.Contains(logStr, `"action":"flagged"`) {
		t.Errorf("audit log missing flagged record:\n%s", logStr)
	}
	if !strings.Contains(logStr, a.ID) || !strings.Contains(logStr, b.ID) {
		t.Errorf("audit log missing the pair (%s,%s):\n%s", a.ID, b.ID, logStr)
	}
	for _, m := range active {
		if len(m.ConflictsWith) == 0 {
			t.Errorf("memory %s missing conflicts_with marker after Cleanup flagged it", m.ID)
		}
	}
}

// TestPhase8_ResolveDryRunNoWrites (§6.1): a dry-run pass that would supersede a
// pair must not touch the vault or either audit log.
func TestPhase8_ResolveDryRunNoWrites(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Type: "decision", Project: "p", Score: 4.0, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Type: "decision", Project: "p", Score: 2.0, Body: "do not use pooling"})

	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Similarity: 0.8, Source: "manual"}}

	res, err := ResolveContradictions(cands, ResolveOptions{DryRun: true, AutoEvict: true})
	if err != nil {
		t.Fatalf("dry-run ResolveContradictions: %v", err)
	}
	if len(res) != 1 || res[0].Action != "superseded" {
		t.Fatalf("dry-run result = %+v, want one superseded", res)
	}
	// The candidate metadata is echoed back on the result (C1 field additions).
	if res[0].Source != "manual" || res[0].Confidence != 0.9 || res[0].Similarity != 0.8 {
		t.Errorf("dry-run result missing candidate metadata: %+v", res[0])
	}
	// Loser file must remain untouched at its original path.
	if _, err := os.Stat(loser.Path); err != nil {
		t.Errorf("dry-run moved/removed loser file: %v", err)
	}
	// No contradiction audit log created.
	if auditLogExists(t) {
		t.Errorf("dry-run wrote %s, must write nothing", contradictionsLog)
	}
	// No promotion audit events.
	events, err := ReadPromotionAudit(now())
	if err != nil {
		t.Fatalf("ReadPromotionAudit: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("dry-run wrote %d promotion audit events, want 0", len(events))
	}
}

// TestPhase8_ListConflicts (§2.1/§3.2): ListConflicts returns the current
// pending pairs as a read-only view, honouring the filters.
func TestPhase8_ListConflicts(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	// A same-project polarity-flip pair the lexical detector will surface.
	placeMemory(t, Memory{ID: "mem_pool_yes", Project: "p", Score: 3.0, Body: "use connection pooling for the database"})
	placeMemory(t, Memory{ID: "mem_pool_no0", Project: "p", Score: 3.0, Body: "do not use connection pooling for the database"})

	got, err := ListConflicts(ListConflictFilter{})
	if err != nil {
		t.Fatalf("ListConflicts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListConflicts returned %d pairs, want 1 (%+v)", len(got), got)
	}
	if got[0].Source != "lexical" {
		t.Errorf("conflict source = %q, want lexical", got[0].Source)
	}
	// ListConflicts must not write anything.
	if auditLogExists(t) {
		t.Errorf("ListConflicts wrote %s, must be read-only", contradictionsLog)
	}
	// Project filter that matches nothing yields an empty list.
	none, err := ListConflicts(ListConflictFilter{Project: "other"})
	if err != nil {
		t.Fatalf("ListConflicts(project filter): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("project filter returned %d, want 0", len(none))
	}
}

// TestPhase8_ResolveApplyEvictThroughRevoke (§6.2): an apply+evict pass routes
// the loser through promote.Revoke, leaving exactly one promotion audit event
// and one contradiction log line cross-referenced by event id.
func TestPhase8_ResolveApplyEvictThroughRevoke(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Project: "p", Score: 4.0, PromotionState: PromotionPromoted, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Project: "p", Score: 2.0, PromotionState: PromotionPromoted, Body: "do not use pooling"})

	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Similarity: 0.8, Source: "manual"}}
	res, err := ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: PromotionActor{Kind: "human", Name: "alice"}})
	if err != nil {
		t.Fatalf("apply ResolveContradictions: %v", err)
	}
	if res[0].Action != "superseded" || res[0].Loser != loser.ID {
		t.Fatalf("apply result = %+v, want loser superseded", res[0])
	}
	evtID := res[0].PromotionAuditEventID
	if evtID == "" {
		t.Fatal("apply result missing promotion audit event id")
	}

	// Loser is superseded with the phase-7 promotion state.
	sup, _ := readAllMemories("superseded", "")
	if len(sup) != 1 || sup[0].SupersededBy != winner.ID || sup[0].PromotionState != PromotionSuperseded {
		t.Fatalf("loser not superseded via revoke: %+v", sup)
	}
	// Winner keeps the supersedes link.
	act, _ := readAllMemories("active", "")
	if len(act) != 1 || act[0].ID != winner.ID || !containsString(act[0].Supersedes, loser.ID) {
		t.Fatalf("winner supersedes not updated: %+v", act)
	}

	// Promotion audit has exactly one revoked event for the loser.
	events, err := ReadPromotionAudit(now())
	if err != nil {
		t.Fatalf("ReadPromotionAudit: %v", err)
	}
	var revoked []PromotionAuditEvent
	for _, e := range events {
		if e.EventType == PromotionEventRevoked && e.SourceID == loser.ID {
			revoked = append(revoked, e)
		}
	}
	if len(revoked) != 1 {
		t.Fatalf("promotion audit revoked events = %d, want 1", len(revoked))
	}
	if revoked[0].FromState != PromotionPromoted || revoked[0].ToState != PromotionSuperseded {
		t.Errorf("revoked event states = %s->%s, want promoted->superseded", revoked[0].FromState, revoked[0].ToState)
	}
	if revoked[0].EventID != evtID {
		t.Errorf("result event id %q != audit event id %q", evtID, revoked[0].EventID)
	}
	// G7: rationale is prefixed so forensics can tell this is contradiction-driven.
	if !strings.HasPrefix(revoked[0].Rationale, "contradiction:") {
		t.Errorf("revoked rationale = %q, want contradiction: prefix", revoked[0].Rationale)
	}
}

// TestPhase8_UnsupersedeRestoresAndReindexes (§6.3): unsupersede restores the
// loser to active/promoted, clears the winner link, and writes an unsuperseded
// promotion audit event.
func TestPhase8_UnsupersedeRestoresAndReindexes(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Project: "p", Score: 4.0, PromotionState: PromotionPromoted, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Project: "p", Score: 2.0, PromotionState: PromotionPromoted, Body: "do not use pooling"})
	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Source: "manual"}}
	if _, err := ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: PromotionActor{Kind: "human", Name: "alice"}}); err != nil {
		t.Fatalf("supersede setup: %v", err)
	}

	restored, err := Unsupersede(loser.ID, "detector false positive", PromotionActor{Kind: "human", Name: "alice"})
	if err != nil {
		t.Fatalf("Unsupersede: %v", err)
	}
	if restored.Status != "active" || restored.PromotionState != PromotionPromoted {
		t.Errorf("restored not active/promoted: %+v", restored)
	}
	if restored.SupersededBy != "" || restored.SupersededAt != "" || restored.SupersededReason != "" {
		t.Errorf("restored supersede fields not cleared: %+v", restored)
	}
	// Winner supersedes list no longer contains the loser.
	act, _ := readAllMemories("active", "")
	for _, m := range act {
		if m.ID == winner.ID && containsString(m.Supersedes, loser.ID) {
			t.Errorf("winner still supersedes restored loser: %v", m.Supersedes)
		}
	}
	// Promotion audit has the unsuperseded event.
	events, err := ReadPromotionAudit(now())
	if err != nil {
		t.Fatalf("ReadPromotionAudit: %v", err)
	}
	var un []PromotionAuditEvent
	for _, e := range events {
		if e.EventType == PromotionEventUnsuperseded && e.SourceID == loser.ID {
			un = append(un, e)
		}
	}
	if len(un) != 1 {
		t.Fatalf("unsuperseded events = %d, want 1", len(un))
	}
	if un[0].FromState != PromotionSuperseded || un[0].ToState != PromotionPromoted {
		t.Errorf("unsuperseded states = %s->%s, want superseded->promoted", un[0].FromState, un[0].ToState)
	}
}

// TestPhase8_AuditDualLogsAlignment (§6.4): repeated supersede/unsupersede/
// supersede keeps the promotion audit and the contradiction log in lockstep,
// with every contradiction line cross-referencing an existing promotion event.
func TestPhase8_AuditDualLogsAlignment(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Project: "p", Score: 4.0, PromotionState: PromotionPromoted, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Project: "p", Score: 2.0, PromotionState: PromotionPromoted, Body: "do not use pooling"})
	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Source: "manual"}}
	actor := PromotionActor{Kind: "human", Name: "alice"}

	if _, err := ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: actor}); err != nil {
		t.Fatalf("first supersede: %v", err)
	}
	if _, err := Unsupersede(loser.ID, "reversal", actor); err != nil {
		t.Fatalf("unsupersede: %v", err)
	}
	if _, err := ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: actor}); err != nil {
		t.Fatalf("second supersede: %v", err)
	}

	events, err := ReadPromotionAudit(now())
	if err != nil {
		t.Fatalf("ReadPromotionAudit: %v", err)
	}
	promoEventIDs := map[string]bool{}
	var revoked, unsuperseded int
	for _, e := range events {
		promoEventIDs[e.EventID] = true
		switch e.EventType {
		case PromotionEventRevoked:
			revoked++
		case PromotionEventUnsuperseded:
			unsuperseded++
		}
	}
	if revoked != 2 || unsuperseded != 1 {
		t.Fatalf("promotion audit = %d revoked / %d unsuperseded, want 2/1", revoked, unsuperseded)
	}

	// Every contradiction line that carries a promotion_audit_event_id must point
	// at a real promotion event.
	raw, err := os.ReadFile(filepath.Join(VaultDir, contradictionsLog))
	if err != nil {
		t.Fatalf("read contradiction log: %v", err)
	}
	stateChanging := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec auditRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode contradiction line: %v", err)
		}
		if rec.Action == "superseded" || rec.Action == "unsuperseded" {
			stateChanging++
			if rec.PromotionAuditEventID == "" {
				t.Errorf("%s line missing promotion_audit_event_id: %s", rec.Action, line)
				continue
			}
			if !promoEventIDs[rec.PromotionAuditEventID] {
				t.Errorf("%s line references unknown promotion event %q", rec.Action, rec.PromotionAuditEventID)
			}
		}
	}
	if stateChanging != 3 {
		t.Errorf("state-changing contradiction lines = %d, want 3", stateChanging)
	}
}

// TestPhase8_SupersedeGoesThroughRevokeOnly (§6.5): the eviction path calls
// Revoke exactly once per superseded pair, so all promotion-state mutation flows
// through the single Revoke chokepoint.
func TestPhase8_SupersedeGoesThroughRevokeOnly(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Project: "p", Score: 4.0, PromotionState: PromotionPromoted, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Project: "p", Score: 2.0, PromotionState: PromotionPromoted, Body: "do not use pooling"})
	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Source: "manual"}}

	if _, err := ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: PromotionActor{Kind: "human", Name: "alice"}}); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	// Exactly one revoked promotion event proves the loser's state transition ran
	// through Revoke once and only once (no second direct-write path).
	events, err := ReadPromotionAudit(now())
	if err != nil {
		t.Fatalf("ReadPromotionAudit: %v", err)
	}
	revoked := 0
	for _, e := range events {
		if e.EventType == PromotionEventRevoked && e.SourceID == loser.ID {
			revoked++
		}
	}
	if revoked != 1 {
		t.Errorf("revoke calls (via audit) = %d, want exactly 1", revoked)
	}
}

// TestPhase8_RefusesL1Supersede (§7.2 G3): a contradiction pass must refuse to
// supersede a curated global (l1) memory.
func TestPhase8_RefusesL1Supersede(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Project: "p", Layer: "l1", Score: 4.0, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Project: "p", Score: 2.0, Body: "do not use pooling"})
	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Source: "manual"}}

	_, err := ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: PromotionActor{Kind: "human", Name: "alice"}})
	if err == nil || !strings.Contains(err.Error(), "l1 memory must be curated") {
		t.Fatalf("l1 supersede error = %v, want refusal", err)
	}
	// Nothing was superseded.
	if sup, _ := readAllMemories("superseded", ""); len(sup) != 0 {
		t.Errorf("l1 refusal still superseded %d memories", len(sup))
	}
}

// TestPhase8_ConcurrentResolveOnSamePair (§6.7): two goroutines racing to
// supersede the same pair must leave exactly one audit record and one archived
// loser (no double eviction). Run with -race.
func TestPhase8_ConcurrentResolveOnSamePair(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Project: "p", Score: 4.0, PromotionState: PromotionPromoted, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Project: "p", Score: 2.0, PromotionState: PromotionPromoted, Body: "do not use pooling"})
	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Source: "manual"}}
	actor := PromotionActor{Kind: "human", Name: "alice"}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: actor})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}

	// Exactly one revoked event: the second pass sees the loser already gone.
	events, err := ReadPromotionAudit(now())
	if err != nil {
		t.Fatalf("ReadPromotionAudit: %v", err)
	}
	revoked := 0
	for _, e := range events {
		if e.EventType == PromotionEventRevoked && e.SourceID == loser.ID {
			revoked++
		}
	}
	if revoked != 1 {
		t.Errorf("concurrent supersede produced %d revoke events, want 1", revoked)
	}
	// Loser superseded exactly once, winner supersedes list has no duplicate.
	if sup, _ := readAllMemories("superseded", ""); len(sup) != 1 {
		t.Errorf("superseded count = %d, want 1", len(sup))
	}
	act, _ := readAllMemories("active", "")
	for _, m := range act {
		if m.ID == winner.ID {
			n := 0
			for _, s := range m.Supersedes {
				if s == loser.ID {
					n++
				}
			}
			if n != 1 {
				t.Errorf("winner supersedes has %d copies of loser, want 1", n)
			}
		}
	}
}

// TestPhase8_RunResolvePairNotPending (P1-3): a --pair that is not a pending
// contradiction must return an explicit error instead of a silent all-zero
// success, and distinguish a non-active member from a merely non-conflicting
// active pair.
func TestPhase8_RunResolvePairNotPending(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	// Two active, unrelated memories: no lexical contradiction between them.
	placeMemory(t, Memory{ID: "mem_alpha0", Project: "p", Score: 3.0, PromotionState: PromotionPromoted, Body: "prefer structured logging in services"})
	placeMemory(t, Memory{ID: "mem_beta00", Project: "p", Score: 3.0, PromotionState: PromotionPromoted, Body: "database migrations run in the deploy step"})

	// Both active but not in conflict → explicit not-pending error.
	if _, err := RunResolve(ResolveSpec{Pair: [2]string{"mem_alpha0", "mem_beta00"}, Evict: true}); err == nil || !strings.Contains(err.Error(), "not a pending contradiction") {
		t.Fatalf("non-conflicting pair error = %v, want not-a-pending-contradiction", err)
	}
	// A member that does not exist → not-active error.
	if _, err := RunResolve(ResolveSpec{Pair: [2]string{"mem_alpha0", "mem_missing"}, Evict: true}); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("missing member error = %v, want not-active", err)
	}
}

// TestPhase8_RunResolveUnboundedRequiresIKnow (P1-1/P1-2): an apply pass with
// --max-pairs 0 is unbounded and must be refused unless --i-know is set,
// regardless of surface (CLI or API both route through RunResolve).
func TestPhase8_RunResolveUnboundedRequiresIKnow(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	placeMemory(t, Memory{ID: "mem_pool_yes", Project: "p", Score: 4.0, PromotionState: PromotionPromoted, Body: "always enable the database connection pooling layer for every service"})
	placeMemory(t, Memory{ID: "mem_pool_no0", Project: "p", Score: 2.0, PromotionState: PromotionPromoted, Body: "never enable the database connection pooling layer for every service"})
	actor := PromotionActor{Kind: "human", Name: "alice"}

	// Apply + unbounded without --i-know is refused.
	if _, err := RunResolve(ResolveSpec{All: true, Evict: true, Apply: true, MaxPairs: 0, Actor: actor}); err == nil || !strings.Contains(err.Error(), "unbounded") {
		t.Fatalf("unbounded apply error = %v, want unbounded refusal", err)
	}
	// The same spec with --i-know is allowed.
	if _, err := RunResolve(ResolveSpec{All: true, Evict: true, Apply: true, MaxPairs: 0, IKnow: true, Actor: actor}); err != nil {
		t.Fatalf("unbounded apply with i-know: %v", err)
	}
	// A dry-run (no apply) is never gated by the batch cap.
	if _, err := RunResolve(ResolveSpec{All: true, Evict: true, Apply: false, MaxPairs: 0}); err != nil {
		t.Fatalf("dry-run unbounded should not be gated: %v", err)
	}
}

// TestPhase8_UnsupersedeAuditFailureRollsBack (P0-2): if the promotion audit
// append fails after the loser file has been rewritten active, the reversal
// must roll back so the loser stays superseded — it must never be active
// without a durable unsuperseded audit record.
func TestPhase8_UnsupersedeAuditFailureRollsBack(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Project: "p", Score: 4.0, PromotionState: PromotionPromoted, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Project: "p", Score: 2.0, PromotionState: PromotionPromoted, Body: "do not use pooling"})
	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Source: "manual"}}
	actor := PromotionActor{Kind: "human", Name: "alice"}
	if _, err := ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: actor}); err != nil {
		t.Fatalf("supersede setup: %v", err)
	}

	// Force appendPreparedAudit to fail after the loser is rewritten active:
	// replace the audit month directory with a regular file so its MkdirAll
	// cannot recreate the path (robust even when the test runs as root).
	auditFile := PromotionAuditPath(now())
	monthDir := filepath.Dir(auditFile)
	if err := os.RemoveAll(monthDir); err != nil {
		t.Fatalf("clear audit month dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(monthDir), 0o755); err != nil {
		t.Fatalf("mkdir audit year dir: %v", err)
	}
	if err := os.WriteFile(monthDir, []byte("blocker"), 0o644); err != nil {
		t.Fatalf("plant audit blocker file: %v", err)
	}
	t.Cleanup(func() { os.Remove(monthDir) })

	if _, err := Unsupersede(loser.ID, "false positive", actor); err == nil {
		t.Fatal("Unsupersede must fail when the audit append fails")
	}
	os.Remove(monthDir)

	// Loser rolled back to superseded, not left dangling active.
	sup, _ := readAllMemories("superseded", "")
	if len(sup) != 1 || sup[0].ID != loser.ID || sup[0].SupersededBy != winner.ID {
		t.Fatalf("loser not rolled back to superseded: %+v", sup)
	}
	act, _ := readAllMemories("active", "")
	for _, m := range act {
		if m.ID == loser.ID {
			t.Fatalf("loser left active without audit after failed reversal")
		}
	}
	// Winner still supersedes the loser (its rollback re-applied the link).
	winnerActive := false
	for _, m := range act {
		if m.ID == winner.ID {
			winnerActive = true
			if !containsString(m.Supersedes, loser.ID) {
				t.Errorf("winner lost supersedes link after rollback: %v", m.Supersedes)
			}
		}
	}
	if !winnerActive {
		t.Fatalf("winner missing from active set after rollback: %+v", act)
	}
	// No unsuperseded promotion event was committed.
	events, _ := ReadPromotionAudit(now())
	for _, e := range events {
		if e.EventType == PromotionEventUnsuperseded {
			t.Fatalf("unsuperseded audit written despite failed reversal: %+v", e)
		}
	}
}

// TestPhase8_SupersedeRevokeFailureRollsBackWinner (P0-1): if the loser
// transition (Revoke) fails after the winner write, the winner must not retain
// a dangling Supersedes link or a cleared conflict marker, the loser must stay
// active, and a supersede_failed line must be recorded so the aborted attempt
// is auditable.
func TestPhase8_SupersedeRevokeFailureRollsBackWinner(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	winner := placeMemory(t, Memory{ID: "mem_winner1", Project: "p", Score: 4.0, PromotionState: PromotionPromoted, ConflictsWith: []string{"mem_loser01"}, Body: "use connection pooling"})
	loser := placeMemory(t, Memory{ID: "mem_loser01", Project: "p", Score: 2.0, PromotionState: PromotionPromoted, ConflictsWith: []string{"mem_winner1"}, Body: "do not use pooling"})

	// Fail the loser's supersede write (Revoke) while letting the winner write
	// through, forcing the mid-commit failure this fix must recover from.
	prev := writeMemory
	writeMemory = func(mem Memory, targetDir string) (string, error) {
		if mem.ID == loser.ID && mem.PromotionState == PromotionSuperseded {
			return "", fmt.Errorf("injected loser supersede write failure")
		}
		return prev(mem, targetDir)
	}
	t.Cleanup(func() { writeMemory = prev })

	cands := []Contradiction{{A: winner.ID, B: loser.ID, Confidence: 0.9, Similarity: 0.8, Source: "manual"}}
	_, err := ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true, Actor: PromotionActor{Kind: "human", Name: "alice"}})
	if err == nil {
		t.Fatal("supersede must fail when the loser transition fails")
	}

	// Restore the real writer so the assertions can read state back.
	writeMemory = prev

	// Winner rolled back: no dangling Supersedes, conflict marker still present.
	act, _ := readAllMemories("active", "")
	var w *Memory
	for i := range act {
		if act[i].ID == winner.ID {
			w = &act[i]
		}
	}
	if w == nil {
		t.Fatalf("winner no longer active after rollback: %+v", act)
	}
	if containsString(w.Supersedes, loser.ID) {
		t.Errorf("winner retains dangling Supersedes after failed revoke: %v", w.Supersedes)
	}
	if !containsString(w.ConflictsWith, loser.ID) {
		t.Errorf("winner conflict marker was cleared despite failed revoke: %v", w.ConflictsWith)
	}
	// Loser stayed active (never superseded).
	if sup, _ := readAllMemories("superseded", ""); len(sup) != 0 {
		t.Errorf("loser superseded despite failed revoke: %+v", sup)
	}

	// A failure line is present so the aborted supersede is auditable.
	raw, err := os.ReadFile(filepath.Join(VaultDir, contradictionsLog))
	if err != nil {
		t.Fatalf("read contradiction log: %v", err)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec auditRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode contradiction line: %v", err)
		}
		if rec.Action == "supersede_failed" && rec.Loser == loser.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("no supersede_failed audit line for aborted supersede")
	}
}

func TestPairKeyCanonical(t *testing.T) {
	c1 := Contradiction{A: "mem_b", B: "mem_a"}
	c2 := Contradiction{A: "mem_a", B: "mem_b"}
	if c1.PairKey() != c2.PairKey() {
		t.Errorf("PairKey not order-independent: %q vs %q", c1.PairKey(), c2.PairKey())
	}
	if c1.PairKey() != "mem_a|mem_b" {
		t.Errorf("PairKey = %q, want mem_a|mem_b", c1.PairKey())
	}
}
