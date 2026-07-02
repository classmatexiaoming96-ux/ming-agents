package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIngestWithOptions_PersistsProvenanceAndInject is the Phase 1 acceptance:
// the options entry point must persist layer, provenance, inject, scope, and
// parent links through frontmatter — the fields the old positional Ingest could
// not reach.
func TestIngestWithOptions_PersistsProvenanceAndInject(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	content := "决定 because 我们采用 connection pooling with 30000 ms timeout\n```go\npool.Max = 10\n```"
	res, err := IngestWithOptions(content, IngestOptions{
		Type:              "decision",
		Project:           "ming-agents",
		Tags:              []string{"db", "pool"},
		Source:            "preloaded",
		Inject:            "always",
		Layer:             "l1",
		ExperienceKind:    "principle",
		SourceSystem:      "operator-preload",
		SourceGranularity: "team",
		ScopeProject:      "ming-agents",
		ScopeRunID:        "run-42",
		ScopePhase:        "planning",
		Parents:           []string{"mem_parent1"},
		BlockedParents:    []string{"mem_blocked1"},
	})
	if err != nil {
		t.Fatalf("IngestWithOptions: %v", err)
	}

	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read written memory: %v", err)
	}
	mem, _, err := parseFrontmatter(string(raw))
	if err != nil {
		t.Fatalf("parse frontmatter: %v", err)
	}

	if mem.Inject != "always" {
		t.Errorf("inject = %q, want always", mem.Inject)
	}
	if mem.Layer != "l1" {
		t.Errorf("layer = %q, want l1", mem.Layer)
	}
	if mem.ExperienceKind != "principle" {
		t.Errorf("experience_kind = %q, want principle", mem.ExperienceKind)
	}
	if mem.SourceSystem != "operator-preload" {
		t.Errorf("source_system = %q, want operator-preload", mem.SourceSystem)
	}
	if mem.SourceGranularity != "team" {
		t.Errorf("source_granularity = %q, want team", mem.SourceGranularity)
	}
	if mem.ScopeProject != "ming-agents" || mem.ScopeRunID != "run-42" || mem.ScopePhase != "planning" {
		t.Errorf("scope = %q/%q/%q, want ming-agents/run-42/planning", mem.ScopeProject, mem.ScopeRunID, mem.ScopePhase)
	}
	if len(mem.Parents) != 1 || mem.Parents[0] != "mem_parent1" {
		t.Errorf("parents = %v, want [mem_parent1]", mem.Parents)
	}
	if len(mem.BlockedParents) != 1 || mem.BlockedParents[0] != "mem_blocked1" {
		t.Errorf("blocked_parents = %v, want [mem_blocked1]", mem.BlockedParents)
	}
	if mem.Source != "preloaded" {
		t.Errorf("source = %q, want preloaded", mem.Source)
	}
}

// TestIngestWithOptions_DefaultsMatchLegacyIngest confirms the options path with
// only the legacy-equivalent fields set produces the same defaults as Ingest:
// inject=query and no provenance fields, so old files stay backward compatible.
func TestIngestWithOptions_DefaultsMatchLegacyIngest(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	content := "决定 because 我们采用 connection pooling with 30000 ms timeout\n```go\npool.Max = 10\n```"
	res, err := IngestWithOptions(content, IngestOptions{
		Type:    "decision",
		Project: "ming-agents",
		Tags:    []string{"db", "pool"},
		Source:  "manual",
	})
	if err != nil {
		t.Fatalf("IngestWithOptions: %v", err)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read written memory: %v", err)
	}
	mem, _, err := parseFrontmatter(string(raw))
	if err != nil {
		t.Fatalf("parse frontmatter: %v", err)
	}
	if mem.Inject != "query" {
		t.Errorf("inject = %q, want query (default)", mem.Inject)
	}
	if mem.Layer != "" || mem.SourceSystem != "" || mem.ExperienceKind != "" {
		t.Errorf("expected empty provenance defaults, got layer=%q source_system=%q experience_kind=%q", mem.Layer, mem.SourceSystem, mem.ExperienceKind)
	}
}

// TestIngest_StillWrapsOptions confirms the legacy positional Ingest keeps
// working and routes through the options path (same id and placement).
func TestIngest_StillWrapsOptions(t *testing.T) {
	vault := useTempVault(t)
	fixedNow(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))

	content := "决定 because 我们采用 connection pooling with 30000 ms timeout\n```go\npool.Max = 10\n```"
	res, err := Ingest(content, "decision", "ming-agents", []string{"db", "pool"}, "manual", "")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !res.Accepted {
		t.Fatalf("expected accepted, got %q", res.Reason)
	}
	wantDir := filepath.Join(vault, "notes", "ming-agents")
	if filepath.Dir(res.Path) != wantDir {
		t.Errorf("path = %s, want under %s", res.Path, wantDir)
	}
	if res.ID != computeID(content) {
		t.Errorf("id = %q, want content-addressed %q", res.ID, computeID(content))
	}
}

// TestIngestWithOptions_PreloadedSourceScored confirms the operator preload
// source resolves to a credibility weight instead of the 0.5 unknown fallback.
func TestIngestWithOptions_PreloadedSourceScored(t *testing.T) {
	if _, ok := sourceScores["preloaded"]; !ok {
		t.Fatal("sourceScores must include preloaded so operator seed is not scored as unknown (0.5)")
	}
}
