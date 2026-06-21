package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRecallCJKQueryFallback is the A1 regression: a Chinese query that FTS5's
// unicode61 tokenizer fails to MATCH must still fall back to substring matching
// instead of returning an empty set. Before the fix, a non-error/empty FTS
// result produced a non-nil empty candidate map that filtered out everything.
func TestRecallCJKQueryFallback(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	mustIngest(t, "数据库连接池因为泄漏而被耗尽，需要在处理函数里释放连接", "incident", "p", []string{"db"}, "manual")
	mustIngest(t, "unrelated english note about caching layers", "snippet", "p", []string{"cache"}, "manual")

	got, _, err := Recall("连接池", "", "", nil, 0, "active", 10)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CJK query returned %d results, want 1 (A1 fallback broken): %v", len(got), titles(got))
	}
	if !strings.Contains(got[0].Body, "连接池") {
		t.Errorf("wrong memory recalled: %q", got[0].Title)
	}
}

// TestRecallFindsInboxByProject is the A5 regression: a below-threshold memory
// living in inbox/ (status=active) must remain visible to a project-scoped
// Recall. The old directory-only project filter looked solely in notes/{project}
// and never scanned inbox.
func TestRecallFindsInboxByProject(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	// Force an inbox memory: duplicate bland content → low novelty → score < 3.
	bland := "misc note about something"
	mustIngest(t, bland, "agent-trace", "proj", []string{"x"}, "manual")
	res := mustIngest(t, bland, "agent-trace", "proj", []string{}, "agent-run")
	if res.Accepted {
		t.Fatalf("setup: expected inbox memory, got accepted score=%g", res.Score)
	}

	got, _, err := Recall("", "proj", "", nil, 0, "active", 10)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	foundInbox := false
	for _, m := range got {
		if filepath.Base(filepath.Dir(m.Path)) == "inbox" {
			foundInbox = true
		}
	}
	if !foundInbox {
		t.Errorf("project-scoped Recall did not surface the inbox memory (A5): %v", titles(got))
	}
}

// TestImplicitContainmentReference is the A3 regression: a memory quoted inside
// a much longer conversation must clear the reference threshold. Under the old
// whole-conversation Jaccard the score was ~|mem|/|reply| and never reached 0.5.
func TestImplicitContainmentReference(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	mem := "use pgx connection pooling with a 30 second timeout"
	res := mustIngest(t, mem, "decision", "p", []string{"db"}, "manual")

	// A long turn that quotes the memory verbatim plus lots of unrelated text.
	filler := strings.Repeat("the assistant discussed many other unrelated topics here. ", 40)
	log := filler + mem + ". " + filler

	out, err := ImplicitFeedback([]string{res.ID}, log)
	if err != nil {
		t.Fatalf("ImplicitFeedback: %v", err)
	}
	if len(out) != 1 || !out[0].Found {
		t.Fatalf("memory not found: %+v", out)
	}
	if out[0].Outcome != "confirmed" {
		t.Errorf("outcome = %q (ref=%.3f), want confirmed — containment reference broken (A3)", out[0].Outcome, out[0].Reference)
	}
	if out[0].Reference < implicitRefThreshold {
		t.Errorf("reference %.3f < threshold %.2f despite verbatim quote", out[0].Reference, implicitRefThreshold)
	}
}

// TestScopedContradictionHedge is the A2 regression: a soft hedge (其实/actually)
// in the referencing sentence must suppress contradiction detection, and the
// CJK hedge must actually match (the old strings.Fields never tokenised it).
func TestScopedContradictionHedge(t *testing.T) {
	mem := "数据库连接池应该使用 pgx"

	// Hard correction without a hedge → contradiction.
	if !scopedContradiction(mem, "数据库连接池不对，配置写错了") {
		t.Error("expected contradiction for hard CJK correction")
	}
	// Same correction word but preceded by a soft hedge → suppressed.
	if scopedContradiction(mem, "其实数据库连接池不对的说法值得商榷") {
		t.Error("soft hedge 其实 should suppress contradiction (A2)")
	}
	// English hedge path.
	if scopedContradiction("use pgx connection pooling", "actually the pgx connection pooling claim is not correct maybe") {
		t.Error("soft hedge 'actually' should suppress contradiction (A2)")
	}
}

// TestIngestPreservesCountersOnReingest is the A4 regression: re-ingesting
// identical content (same id) must not wipe the evidence/usage counters that
// survivorScore depends on.
func TestIngestPreservesCountersOnReingest(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	content := "decision: adopt connection pooling because of 30000 ms timeouts\n```go\npool.Max=10\n```"
	first := mustIngest(t, content, "decision", "p", []string{"db", "pool"}, "manual")

	// Accrue explicit evidence.
	if _, err := Feedback(first.ID, true, true); err != nil {
		t.Fatalf("Feedback: %v", err)
	}

	// Re-ingest identical content.
	second := mustIngest(t, content, "decision", "p", []string{"db", "pool"}, "manual")
	if second.ID != first.ID {
		t.Fatalf("identical content produced different ids: %s vs %s", first.ID, second.ID)
	}

	mems, _ := readAllMemories("active", "p")
	var got *Memory
	for i := range mems {
		if mems[i].ID == first.ID {
			got = &mems[i]
		}
	}
	if got == nil {
		t.Fatal("memory missing after re-ingest")
	}
	if got.HitCount != 1 || got.ExplicitHits != 1 {
		t.Errorf("re-ingest wiped counters: hit_count=%d explicit_hits=%d, want 1/1 (A4)", got.HitCount, got.ExplicitHits)
	}
}

// TestDedupeByIDPrefersLater is the C3 regression: if a crash left the same id
// in two places, readAllMemories must not double-count it.
func TestDedupeByIDPrefersLater(t *testing.T) {
	in := []Memory{
		{ID: "mem_x", Status: "active", Project: "p"},
		{ID: "mem_y", Status: "active", Project: "p"},
		{ID: "mem_x", Status: "archived", Project: "p"}, // orphaned duplicate
	}
	out := dedupeByID(in)
	if len(out) != 2 {
		t.Fatalf("dedupeByID returned %d, want 2", len(out))
	}
	for _, m := range out {
		if m.ID == "mem_x" && m.Status != "archived" {
			t.Errorf("dedupeByID kept %q copy, want the later (archived) one", m.Status)
		}
	}
}

func titles(ms []Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Title
	}
	return out
}
