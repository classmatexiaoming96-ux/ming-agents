package memory

import (
	"os"
	"path/filepath"
	"strings"
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
	active, _ := readAllMemories("active")
	if len(active) != 2 {
		t.Errorf("DryRun mutated vault: %d active memories, want 2", len(active))
	}
	if auditLogExists(t) {
		t.Errorf("DryRun wrote %s, must write nothing", contradictionsLog)
	}

	// Real run: loser is superseded and moved to archive; winner gains supersedes.
	res, err = ResolveContradictions(cands, ResolveOptions{DryRun: false, AutoEvict: true})
	if err != nil {
		t.Fatalf("real ResolveContradictions: %v", err)
	}
	if res[0].Action != "superseded" {
		t.Fatalf("real action = %q, want superseded", res[0].Action)
	}
	if _, err := os.Stat(loser.Path); !os.IsNotExist(err) {
		t.Errorf("loser file still at original path %s", loser.Path)
	}
	sup, _ := readAllMemories("superseded")
	if len(sup) != 1 || sup[0].ID != loser.ID {
		t.Fatalf("expected loser superseded, got %+v", sup)
	}
	if sup[0].SupersededBy != winner.ID || sup[0].SupersededAt == "" || sup[0].SupersededReason == "" {
		t.Errorf("superseded metadata incomplete: %+v", sup[0])
	}
	if filepath.Dir(sup[0].Path) != filepath.Join(VaultDir, "archive", "p") {
		t.Errorf("loser not in archive/p: %s", sup[0].Path)
	}
	act, _ := readAllMemories("active")
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
	restored, err := Unsupersede(loser.ID)
	if err != nil {
		t.Fatalf("Unsupersede: %v", err)
	}
	if restored.Status != "active" || restored.SupersededBy != "" {
		t.Errorf("restored memory not clean: %+v", restored)
	}
	act, _ = readAllMemories("active")
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
	if sup, _ := readAllMemories("superseded"); len(sup) != 0 {
		t.Errorf("low-confidence pass superseded %d memories, want 0", len(sup))
	}
	// Both sides carry the durable conflicts_with marker.
	act, _ := readAllMemories("active")
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
	if sup, _ := readAllMemories("superseded"); len(sup) != 0 {
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
	if sup, _ := readAllMemories("superseded"); len(sup) != 0 {
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
