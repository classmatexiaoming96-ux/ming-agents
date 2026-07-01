package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ming-agents/server/memory"
)

func TestCmdImportAutoMindSummary_DryRunDoesNotWrite(t *testing.T) {
	vault := useTempCLIVault(t)
	summary := writeCLISummaryFixture(t)

	var out bytes.Buffer
	if err := cmdImportAutoMindSummary([]string{summary}, &out); err != nil {
		t.Fatalf("cmdImportAutoMindSummary() error = %v", err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Fatalf("output = %q, want dry-run marker", out.String())
	}
	for _, rel := range []string{
		filepath.Join("notes", "ming-agents"),
		filepath.Join("runs", "ming-agents", "run-cli"),
		filepath.Join("notes", "_inbox", "cross_project_candidates"),
	} {
		if _, err := os.Stat(filepath.Join(vault, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after dry-run: %v", rel, err)
		}
	}
}

func TestCmdImportAutoMindSummary_AcceptWritesRoutes(t *testing.T) {
	vault := useTempCLIVault(t)
	summary := writeCLISummaryFixture(t)

	var out bytes.Buffer
	if err := cmdImportAutoMindSummary([]string{"--accept", summary}, &out); err != nil {
		t.Fatalf("cmdImportAutoMindSummary() error = %v", err)
	}
	output := out.String()
	for _, want := range []string{"l2=1", "l3=1", "inbox=1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want %s", output, want)
		}
	}
	for _, rel := range []string{
		filepath.Join("notes", "ming-agents"),
		filepath.Join("runs", "ming-agents", "run-cli", "summary", "items.jsonl"),
		filepath.Join("notes", "_inbox", "cross_project_candidates"),
	} {
		if _, err := os.Stat(filepath.Join(vault, rel)); err != nil {
			t.Fatalf("%s missing after accept: %v", rel, err)
		}
	}
}

func useTempCLIVault(t *testing.T) string {
	t.Helper()
	prev := memory.VaultDir
	dir := t.TempDir()
	memory.VaultDir = dir
	t.Cleanup(func() { memory.VaultDir = prev })
	return dir
}

func writeCLISummaryFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	rawSummary := filepath.Join(dir, "raw.md")
	if err := os.WriteFile(rawSummary, []byte("# raw\n"), 0o644); err != nil {
		t.Fatalf("write raw summary: %v", err)
	}
	path := filepath.Join(dir, "summary.yaml")
	body := `
run_id: run-cli
project: ming-agents
source_system: automind
summary_path: ` + rawSummary + `
items:
  - kind: durable_lesson
    title: CLI durable lesson
    body: Use the CLI import command because it keeps summary routing explicit.
  - kind: raw_evidence
    title: CLI raw bundle
    body: raw bundle body
  - kind: cross_project_candidate
    title: CLI cross project candidate
    body: review before promoting globally
`
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	return path
}
