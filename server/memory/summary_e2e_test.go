package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportSummary_E2ERoutesAutoMindSummary(t *testing.T) {
	vault := useTempVault(t)
	summaryPath := writeE2ESummaryFixture(t)

	dryRun, err := ImportSummary(summaryPath, SummaryImportOptions{})
	if err != nil {
		t.Fatalf("ImportSummary dry-run error = %v", err)
	}
	if !dryRun.DryRun || dryRun.L2 != 3 || dryRun.L3 != 2 || dryRun.Inbox != 1 {
		t.Fatalf("dry-run result = %+v, want l2=3 l3=2 inbox=1", dryRun)
	}
	assertMissing(t, filepath.Join(vault, "notes", "ming-agents"))
	assertMissing(t, filepath.Join(vault, "runs", "ming-agents", "run-e2e"))
	assertMissing(t, filepath.Join(vault, "notes", "_inbox", "cross_project_candidates"))

	accepted, err := ImportSummary(summaryPath, SummaryImportOptions{Accept: true})
	if err != nil {
		t.Fatalf("ImportSummary accept error = %v", err)
	}
	if accepted.DryRun || accepted.L2 != 3 || accepted.L3 != 2 || accepted.Inbox != 1 {
		t.Fatalf("accept result = %+v, want l2=3 l3=2 inbox=1", accepted)
	}

	memories, err := readAllMemories("active", "ming-agents")
	if err != nil {
		t.Fatalf("read memories: %v", err)
	}
	if len(memories) != 3 {
		t.Fatalf("ming-agents active memories = %d, want 3", len(memories))
	}
	for _, mem := range memories {
		if mem.SourceSystem != "automind" || mem.SourceGranularity != "task_summary" || mem.Layer != "l2" {
			t.Fatalf("memory provenance = %+v", mem)
		}
	}

	l3Items := filepath.Join(vault, "runs", "ming-agents", "run-e2e", "summary", "items.jsonl")
	raw, err := os.ReadFile(l3Items)
	if err != nil {
		t.Fatalf("read l3 items: %v", err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); got != 2 {
		t.Fatalf("raw evidence lines = %d, want 2", got)
	}
	if strings.Contains(l3Items, filepath.Join("archive", "ming-agents")) ||
		strings.Contains(l3Items, filepath.Join("notes", "ming-agents")) {
		t.Fatalf("L3 path mixed with archive/notes: %s", l3Items)
	}

	inboxDir := filepath.Join(vault, "notes", "_inbox", "cross_project_candidates")
	inboxEntries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	if len(inboxEntries) != 1 {
		t.Fatalf("cross-project inbox entries = %d, want 1", len(inboxEntries))
	}

	_, err = ImportSummary(summaryPath, SummaryImportOptions{Accept: true})
	if !errors.Is(err, ErrBundleFrozen) {
		t.Fatalf("duplicate ImportSummary error = %v, want ErrBundleFrozen prompt", err)
	}
}

func writeE2ESummaryFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	rawSummary := filepath.Join(dir, "raw-summary.md")
	if err := os.WriteFile(rawSummary, []byte("# end summary\n"), 0o644); err != nil {
		t.Fatalf("write raw summary: %v", err)
	}
	path := filepath.Join(dir, "summary.yaml")
	body := `
run_id: run-e2e
project: ming-agents
source_system: automind
summary_path: ` + rawSummary + `
items:
  - kind: durable_lesson
    title: Keep receiver tests focused
    body: Test summary receiver behavior at package boundaries because routing spans storage layers.
  - kind: durable_lesson
    title: Preserve raw evidence
    body: Archive raw AutoMind output in L3 because L2 lessons need auditable evidence.
  - kind: durable_lesson
    title: Require explicit accept
    body: Keep imports dry by default because task summaries can contain noisy candidates.
  - kind: raw_evidence
    title: raw command log
    body: go test ./memory/...
  - kind: raw_evidence
    title: reuse ack
    body: accepted reuse memory identifiers
  - kind: cross_project_candidate
    title: curate before global promotion
    body: Cross-project memories require human review before entering shared preload.
`
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	return path
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists, want missing: %v", path, err)
	}
}
