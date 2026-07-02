package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ming-agents/server/memory"
)

func writePreloadFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write preload fixture: %v", err)
	}
	return path
}

// TestCmdPreload_YAMLList imports a top-level YAML list and confirms L1 seed
// lands with inject=always and source=preloaded.
func TestCmdPreload_YAMLList(t *testing.T) {
	vault := useTempCLIVault(t)
	file := writePreloadFile(t, "seed.yaml", `- content: "always prefer table driven tests because they cover edges"
  type: gotcha
  project: ming-agents
  inject: always
  layer: l1
- content: "use pgx pooling for postgres because it reuses connections"
  type: decision
  project: ming-agents
  layer: l2
`)
	var out bytes.Buffer
	if err := cmdPreload([]string{"--layer", "l1", file}, &out); err != nil {
		t.Fatalf("cmdPreload: %v", err)
	}
	if !strings.Contains(out.String(), "preload apply") {
		t.Fatalf("output = %q, want apply marker", out.String())
	}
	// notes/ming-agents must now contain seeded memories.
	notesDir := filepath.Join(vault, "notes", "ming-agents")
	entries, err := os.ReadDir(notesDir)
	if err != nil {
		t.Fatalf("read notes dir: %v", err)
	}
	if len(entries) < 1 {
		t.Fatalf("expected seeded memories under %s, got none", notesDir)
	}
	// Confirm provenance persisted.
	found := false
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(notesDir, e.Name()))
		if strings.Contains(string(raw), "source: preloaded") && strings.Contains(string(raw), "inject: always") {
			found = true
		}
	}
	if !found {
		t.Fatal("no seeded memory with source=preloaded and inject=always")
	}
}

// TestCmdPreload_YAMLMemoriesDoc imports a doc with a top-level memories key.
func TestCmdPreload_YAMLMemoriesDoc(t *testing.T) {
	vault := useTempCLIVault(t)
	file := writePreloadFile(t, "seed.yaml", `memories:
  - content: "prefer context timeouts on all rpc calls because hangs cascade"
    type: gotcha
    project: ming-agents
    layer: l2
`)
	var out bytes.Buffer
	if err := cmdPreload([]string{"--layer", "l2", file}, &out); err != nil {
		t.Fatalf("cmdPreload: %v", err)
	}
	if !strings.Contains(out.String(), "seeded=1") {
		t.Fatalf("output = %q, want seeded=1", out.String())
	}
	if _, err := os.Stat(filepath.Join(vault, "notes", "ming-agents")); err != nil {
		t.Fatalf("notes not written: %v", err)
	}
}

// TestCmdPreload_MarkdownFrontmatter imports a single markdown-frontmatter file
// whose body becomes the memory content.
func TestCmdPreload_MarkdownFrontmatter(t *testing.T) {
	vault := useTempCLIVault(t)
	file := writePreloadFile(t, "seed.md", `---
type: decision
project: ming-agents
inject: always
layer: l1
source_system: operator-preload
---
We standardize on structured logging because grep across services needs stable keys and 12345 fields.
`)
	var out bytes.Buffer
	if err := cmdPreload([]string{file}, &out); err != nil {
		t.Fatalf("cmdPreload: %v", err)
	}
	if !strings.Contains(out.String(), "seeded=1") {
		t.Fatalf("output = %q, want seeded=1", out.String())
	}
	notesDir := filepath.Join(vault, "notes", "ming-agents")
	entries, err := os.ReadDir(notesDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one seeded memory, got %v err=%v", entries, err)
	}
	raw, _ := os.ReadFile(filepath.Join(notesDir, entries[0].Name()))
	if !strings.Contains(string(raw), "source_system: operator-preload") {
		t.Fatalf("markdown provenance not persisted: %s", raw)
	}
	if !strings.Contains(string(raw), "structured logging") {
		t.Fatalf("markdown body not used as content: %s", raw)
	}
}

// TestCmdPreload_JSON imports a JSON array.
func TestCmdPreload_JSON(t *testing.T) {
	vault := useTempCLIVault(t)
	file := writePreloadFile(t, "seed.json", `[
  {"content": "cache invalidation is hard because two writers race", "type": "gotcha", "project": "ming-agents", "layer": "l2"}
]`)
	var out bytes.Buffer
	if err := cmdPreload([]string{file}, &out); err != nil {
		t.Fatalf("cmdPreload: %v", err)
	}
	if !strings.Contains(out.String(), "seeded=1") {
		t.Fatalf("output = %q, want seeded=1", out.String())
	}
	if _, err := os.Stat(filepath.Join(vault, "notes", "ming-agents")); err != nil {
		t.Fatalf("notes not written: %v", err)
	}
}

// TestCmdPreload_DryRunNoWrites confirms --dry-run never touches the vault.
func TestCmdPreload_DryRunNoWrites(t *testing.T) {
	vault := useTempCLIVault(t)
	file := writePreloadFile(t, "seed.yaml", `- content: "dry run seed content because we only preview it here"
  type: decision
  project: ming-agents
  layer: l1
`)
	var out bytes.Buffer
	if err := cmdPreload([]string{"--dry-run", file}, &out); err != nil {
		t.Fatalf("cmdPreload: %v", err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Fatalf("output = %q, want dry-run marker", out.String())
	}
	for _, rel := range []string{"notes", "inbox"} {
		if _, err := os.Stat(filepath.Join(vault, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s exists after dry-run", rel)
		}
	}
}

// TestCmdPreload_SourceAndLayerDefaults confirms source defaults to preloaded
// and the --layer flag fills entries that omit a layer.
func TestCmdPreload_SourceAndLayerDefaults(t *testing.T) {
	vault := useTempCLIVault(t)
	file := writePreloadFile(t, "seed.yaml", `- content: "layer defaulting seed because entry omits an explicit layer here"
  type: decision
  project: ming-agents
`)
	var out bytes.Buffer
	if err := cmdPreload([]string{"--layer", "l1", file}, &out); err != nil {
		t.Fatalf("cmdPreload: %v", err)
	}
	notesDir := filepath.Join(vault, "notes", "ming-agents")
	entries, _ := os.ReadDir(notesDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 seeded memory, got %d", len(entries))
	}
	raw, _ := os.ReadFile(filepath.Join(notesDir, entries[0].Name()))
	if !strings.Contains(string(raw), "layer: l1") {
		t.Fatalf("--layer default not applied: %s", raw)
	}
	if !strings.Contains(string(raw), "source: preloaded") {
		t.Fatalf("source default not preloaded: %s", raw)
	}
}

// TestCmdPreload_StrictFailsOnEmptyContent confirms --strict rejects an entry
// missing content instead of silently skipping it.
func TestCmdPreload_StrictFailsOnEmptyContent(t *testing.T) {
	useTempCLIVault(t)
	file := writePreloadFile(t, "seed.yaml", `- type: decision
  project: ming-agents
`)
	var out bytes.Buffer
	err := cmdPreload([]string{"--strict", file}, &out)
	if err == nil {
		t.Fatal("expected --strict to fail on empty content")
	}
}

var _ = memory.IngestOptions{}
