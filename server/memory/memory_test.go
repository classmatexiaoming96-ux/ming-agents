package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useTempVault points VaultDir at a fresh temp directory for the test and
// restores it afterwards. It also isolates the FTS5 index in the same temp dir
// so tests never read or mutate the developer's real ~/.hermes/memory.fts.db.
func useTempVault(t *testing.T) string {
	t.Helper()
	prevVault := VaultDir
	prevFTS := ftsDB
	dir := t.TempDir()
	VaultDir = dir
	_ = CloseFTS() // drop any cached handle to the previous db
	ftsDB = filepath.Join(dir, "memory.fts.db")
	t.Cleanup(func() {
		_ = CloseFTS()
		VaultDir = prevVault
		ftsDB = prevFTS
	})
	return dir
}

// fixedNow pins the clock for deterministic created_at / expires_at values.
func fixedNow(t *testing.T, ts time.Time) {
	t.Helper()
	prev := now
	now = func() time.Time { return ts }
	t.Cleanup(func() { now = prev })
}

func TestIngestAcceptedHighScore(t *testing.T) {
	vault := useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	// Rich content (code + causal word + number) on an empty vault → novelty 1.0,
	// which comfortably clears the 3.0 threshold.
	content := "决定 because 我们采用 connection pooling with 30000 ms timeout\n```go\npool.Max = 10\n```"
	res, err := Ingest(content, "decision", "ming-agents", []string{"db", "pool"}, "manual", "")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("expected accepted, got reason=%q score=%g", res.Reason, res.Score)
	}
	if res.Score < scoreThreshold {
		t.Fatalf("score %g below threshold %g", res.Score, scoreThreshold)
	}

	// Accepted memories land under notes/{project}.
	wantDir := filepath.Join(vault, "notes", "ming-agents")
	if filepath.Dir(res.Path) != wantDir {
		t.Errorf("path = %s, want under %s", res.Path, wantDir)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Errorf("file not written: %v", err)
	}

	// decision type never expires.
	mems, _ := readAllMemories("active", "")
	if len(mems) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(mems))
	}
	if mems[0].ExpiresAt != neverExpires {
		t.Errorf("expires_at = %s, want %s", mems[0].ExpiresAt, neverExpires)
	}
	if mems[0].Type != "decision" || mems[0].Project != "ming-agents" {
		t.Errorf("unexpected fm: %+v", mems[0])
	}
}

func TestIngestBelowThresholdGoesToInbox(t *testing.T) {
	vault := useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	// Seed an identical note first so the second ingest has near-zero novelty;
	// combined with no tags / bland body that drops the score below 3.0.
	bland := "misc note about something"
	mustIngest(t, bland, "agent-trace", "proj", []string{"x"}, "manual")

	res, err := Ingest(bland, "agent-trace", "proj", []string{}, "agent-run", "")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted {
		t.Fatalf("expected below threshold, got score=%g", res.Score)
	}
	wantDir := filepath.Join(vault, "inbox")
	if filepath.Dir(res.Path) != wantDir {
		t.Errorf("path = %s, want under %s", res.Path, wantDir)
	}
}

func TestIngestNoveltyDropsForDuplicate(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	content := "the database connection pool was exhausted because of a leak in the handler"
	first, err := Ingest(content, "incident", "p", []string{"db"}, "manual", "")
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	// Re-ingesting identical content: novelty collapses to ~0, lowering the score.
	second, err := Ingest(content, "incident", "p", []string{"db"}, "manual", "")
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if second.Score >= first.Score {
		t.Errorf("expected duplicate score %g < original %g", second.Score, first.Score)
	}
}

func TestRecallFiltersAndSorts(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	mustIngest(t, "alpha decision about caching", "decision", "proj-a", []string{"cache"}, "manual")
	mustIngest(t, "beta incident error crash", "incident", "proj-b", []string{"net"}, "agent-run")
	mustIngest(t, "gamma snippet ```code```", "snippet", "proj-a", []string{"go"}, "manual")

	// Filter by project.
	got, _, err := Recall("", "proj-a", "", nil, 0, "active", 10)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("project filter: got %d, want 2", len(got))
	}
	// Sorted by score descending.
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Errorf("results not sorted by score desc: %v", scores(got))
		}
	}

	// Filter by type.
	got, _, _ = Recall("", "", "incident", nil, 0, "active", 10)
	if len(got) != 1 || got[0].Type != "incident" {
		t.Fatalf("type filter failed: %+v", got)
	}

	// Filter by tag.
	got, _, _ = Recall("", "", "", []string{"go"}, 0, "active", 10)
	if len(got) != 1 || got[0].Project != "proj-a" {
		t.Fatalf("tag filter failed: %+v", got)
	}

	// Query keyword (case-insensitive) on body.
	got, _, _ = Recall("CACHING", "", "", nil, 0, "active", 10)
	if len(got) != 1 {
		t.Fatalf("query filter failed: %+v", got)
	}

	// Limit.
	got, _, _ = Recall("", "", "", nil, 0, "active", 1)
	if len(got) != 1 {
		t.Fatalf("limit failed: got %d, want 1", len(got))
	}
}

func TestFeedbackIncrementsHitCountAndScore(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	res := mustIngest(t, "decision about retry strategy because timeouts", "decision", "p", []string{"retry"}, "manual")
	before := res.Score

	fb, err := Feedback(res.ID, true, true)
	if err != nil {
		t.Fatalf("Feedback: %v", err)
	}
	if fb.HitCount != 1 {
		t.Errorf("hit_count = %d, want 1", fb.HitCount)
	}
	// used (+0.05) + helpful (+0.1) = +0.15
	wantScore := round1(before + 0.15)
	if fb.Score != wantScore {
		t.Errorf("score = %g, want %g", fb.Score, wantScore)
	}

	// Second feedback bumps hit_count again and persists.
	fb2, err := Feedback(res.ID, false, false)
	if err != nil {
		t.Fatalf("second Feedback: %v", err)
	}
	if fb2.HitCount != 2 {
		t.Errorf("hit_count = %d, want 2", fb2.HitCount)
	}

	// Missing id is an error.
	if _, err := Feedback("mem_doesnotexist", true, false); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestCleanupArchivesExpired(t *testing.T) {
	vault := useTempVault(t)

	// Ingest an incident as if it were created long ago (TTL 365d).
	fixedNow(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	expired := mustIngest(t, "old incident error crash from 2024", "incident", "proj-x", []string{"old"}, "manual")

	// Ingest a permanent decision (never expires).
	keep := mustIngest(t, "permanent decision 因为 reasons", "decision", "proj-x", []string{"keep"}, "manual")

	// Advance clock well past the incident's expiry.
	fixedNow(t, time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC))

	res, err := Cleanup()
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if res.Archived != 1 {
		t.Fatalf("archived = %d, want 1", res.Archived)
	}

	// Expired file moved out of notes into archive/{project}.
	if _, err := os.Stat(expired.Path); !os.IsNotExist(err) {
		t.Errorf("expired file still present at %s", expired.Path)
	}
	arch, _ := readAllMemories("archived", "")
	if len(arch) != 1 || arch[0].Status != "archived" {
		t.Fatalf("expected 1 archived memory, got %+v", arch)
	}
	if filepath.Dir(arch[0].Path) != filepath.Join(vault, "archive", "proj-x") {
		t.Errorf("archived path = %s, want under archive/proj-x", arch[0].Path)
	}

	// The permanent decision is untouched.
	if _, err := os.Stat(keep.Path); err != nil {
		t.Errorf("permanent memory was removed: %v", err)
	}
}

func TestComputeIDStable(t *testing.T) {
	a := computeID("hello world")
	b := computeID("hello world")
	if a != b {
		t.Errorf("computeID not stable: %s != %s", a, b)
	}
	if computeID("hello world") == computeID("different") {
		t.Error("computeID collision for distinct content")
	}
	// A4: ids are now derived from the full content (no 200-byte prefix cap) and
	// widened to 16 hex. Two contents sharing a long prefix must NOT collide.
	prefix := strings.Repeat("x", 500)
	if computeID(prefix+"A") == computeID(prefix+"B") {
		t.Error("computeID collides on shared 200+ byte prefix (A4 regression)")
	}
	if len(a) != len("mem_")+16 {
		t.Errorf("unexpected id length: %s", a)
	}
}

func TestParseFrontmatterRoundTrip(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	res := mustIngest(t, "round trip 决定 with code ```x```", "decision", "rt", []string{"a", "b"}, "manual")
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	mem, body, err := parseFrontmatter(string(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mem.ID != res.ID || mem.Type != "decision" || mem.Project != "rt" {
		t.Errorf("frontmatter mismatch: %+v", mem)
	}
	if len(mem.Tags) != 2 {
		t.Errorf("tags = %v, want 2", mem.Tags)
	}
	if body == "" {
		t.Error("body empty after round trip")
	}
}

// --- helpers ---

func mustIngest(t *testing.T, content, typ, project string, tags []string, source string) Result {
	t.Helper()
	res, err := Ingest(content, typ, project, tags, source, "")
	if err != nil {
		t.Fatalf("Ingest(%q): %v", content, err)
	}
	return res
}

func scores(ms []Memory) []float64 {
	s := make([]float64, len(ms))
	for i, m := range ms {
		s[i] = m.Score
	}
	return s
}
