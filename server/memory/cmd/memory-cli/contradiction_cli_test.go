package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ming-agents/server/memory"
)

// seedActiveNote writes an active note directly as frontmatter so the CLI
// package needs no unexported memory helper.
func seedActiveNote(t *testing.T, id, project, body string, score float64) {
	t.Helper()
	dir := filepath.Join(memory.VaultDir, "notes", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir notes dir: %v", err)
	}
	content := fmt.Sprintf("---\nid: %s\ntype: decision\nproject: %s\nstatus: active\nlayer: l2\npromotion_state: promoted\nscore: %g\n---\n%s",
		id, project, score, body)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
}

// seedContradictoryPair plants a same-project polarity-flip pair with high
// lexical overlap (confidence above the eviction floor) and a score gap wide
// enough to name a winner, so the detector both surfaces and can supersede it.
func seedContradictoryPair(t *testing.T) (string, string) {
	t.Helper()
	seedActiveNote(t, "mem_pool_yes", "p", "always enable the database connection pooling layer for every service", 4.0)
	seedActiveNote(t, "mem_pool_no0", "p", "never enable the database connection pooling layer for every service", 2.0)
	return "mem_pool_no0", "mem_pool_yes" // canonical A<B
}

func TestCmdConflicts_ListsPending(t *testing.T) {
	useTempCLIVault(t)
	seedContradictoryPair(t)

	var out bytes.Buffer
	if err := cmdConflicts([]string{"--format", "json"}, &out); err != nil {
		t.Fatalf("cmdConflicts: %v", err)
	}
	if !strings.Contains(out.String(), "mem_pool_yes") || !strings.Contains(out.String(), "lexical") {
		t.Fatalf("conflicts output = %q, want the lexical pair", out.String())
	}
	// Read-only: no contradiction log written.
	if _, err := os.Stat(filepath.Join(memory.VaultDir, "_contradictions.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("conflicts wrote the contradiction log: %v", err)
	}
}

func TestCmdResolve_DryRunByDefault(t *testing.T) {
	useTempCLIVault(t)
	a, b := seedContradictoryPair(t)

	var out bytes.Buffer
	if err := cmdResolve([]string{"--pair", a + "," + b, "--evict"}, &out); err != nil {
		t.Fatalf("cmdResolve: %v", err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Fatalf("resolve output = %q, want dry-run", out.String())
	}
	// Default dry-run must not write the contradiction log or promotion audit.
	if _, err := os.Stat(filepath.Join(memory.VaultDir, "_contradictions.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote the contradiction log: %v", err)
	}
	if _, err := os.Stat(memory.PromotionAuditDir()); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote promotion audit: %v", err)
	}
}

func TestCmdResolve_ApplyRequiresActor(t *testing.T) {
	useTempCLIVault(t)
	a, b := seedContradictoryPair(t)

	var out bytes.Buffer
	err := cmdResolve([]string{"--pair", a + "," + b, "--evict", "--apply"}, &out)
	if err == nil || !strings.Contains(err.Error(), "requires --actor") {
		t.Fatalf("apply without --actor error = %v, want actor requirement", err)
	}
}

func TestCmdResolve_MutuallyExclusivePairAll(t *testing.T) {
	useTempCLIVault(t)
	var out bytes.Buffer
	err := cmdResolve([]string{"--pair", "a,b", "--all"}, &out)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("--pair --all error = %v, want mutual exclusion", err)
	}
}

func TestCmdResolve_ApplyEvictThroughRevoke(t *testing.T) {
	useTempCLIVault(t)
	a, b := seedContradictoryPair(t)

	var out bytes.Buffer
	if err := cmdResolve([]string{"--pair", a + "," + b, "--evict", "--apply", "--actor", "alice"}, &out); err != nil {
		t.Fatalf("cmdResolve apply: %v", err)
	}
	if !strings.Contains(out.String(), "superseded") {
		t.Fatalf("apply output = %q, want a superseded pair", out.String())
	}
	if _, err := os.Stat(memory.PromotionAuditDir()); err != nil {
		t.Fatalf("apply did not write promotion audit: %v", err)
	}
}

func TestCmdUnsupersede_DryRunAndApply(t *testing.T) {
	useTempCLIVault(t)
	a, b := seedContradictoryPair(t)

	// First supersede so there is a loser to restore.
	var out bytes.Buffer
	if err := cmdResolve([]string{"--pair", a + "," + b, "--evict", "--apply", "--actor", "alice"}, &out); err != nil {
		t.Fatalf("setup supersede: %v", err)
	}

	// Dry-run unsupersede writes nothing.
	out.Reset()
	if err := cmdUnsupersede([]string{a}, &out); err != nil {
		t.Fatalf("cmdUnsupersede dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Fatalf("unsupersede output = %q, want dry-run", out.String())
	}

	// Apply requires --actor and --reason.
	out.Reset()
	if err := cmdUnsupersede([]string{a, "--apply"}, &out); err == nil {
		t.Fatal("unsupersede --apply without actor/reason must fail")
	}

	// Apply restores the loser.
	out.Reset()
	if err := cmdUnsupersede([]string{a, "--apply", "--actor", "alice", "--reason", "false positive"}, &out); err != nil {
		t.Fatalf("cmdUnsupersede apply: %v", err)
	}
	if !strings.Contains(out.String(), "restored") {
		t.Fatalf("unsupersede apply output = %q, want restored", out.String())
	}
}
