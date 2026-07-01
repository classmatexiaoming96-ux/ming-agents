package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBrief_AlwaysFirst(t *testing.T) {
	useTempVault(t)
	writeBriefMemory(t, Memory{ID: "mem_query", Project: "brief-proj", Title: "Query memory", Score: 9, Status: "active", Source: "manual", Inject: "query"}, "needle query lesson")
	writeBriefMemory(t, Memory{ID: "mem_always", Project: "brief-proj", Title: "Always memory", Score: 1, Status: "active", Source: "manual", Inject: "always"}, "always injected lesson")

	block, audit, err := Brief("brief-proj", "needle", Budget{MaxTokens: 4000})
	if err != nil {
		t.Fatalf("Brief() error = %v", err)
	}
	if audit.AlwaysCount != 1 || audit.QueryCount != 1 {
		t.Fatalf("audit counts = %+v, want one always and one query", audit)
	}
	if got, want := audit.InjectedIDs, []string{"mem_always", "mem_query"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("InjectedIDs = %#v, want %#v", got, want)
	}
	if strings.Index(block, "Always memory") > strings.Index(block, "Query memory") {
		t.Fatalf("block order = %q, want always memory first", block)
	}
}

func TestBrief_BudgetTruncation(t *testing.T) {
	useTempVault(t)
	writeBriefMemory(t, Memory{ID: "mem_large", Project: "brief-proj", Title: "Oversized memory", Score: 9, Status: "active", Source: "manual", Inject: "query"}, strings.Repeat("oversized ", 80))

	block, audit, err := Brief("brief-proj", "oversized", Budget{MaxTokens: 1})
	if err != nil {
		t.Fatalf("Brief() error = %v", err)
	}
	if block != "" {
		t.Fatalf("block = %q, want empty when first query memory exceeds budget", block)
	}
	if !audit.Truncated {
		t.Fatalf("Truncated = false, want true; audit=%+v", audit)
	}
	if audit.TruncatedAt != "mem_large" {
		t.Fatalf("TruncatedAt = %q, want mem_large", audit.TruncatedAt)
	}
	if len(audit.InjectedIDs) != 0 {
		t.Fatalf("InjectedIDs = %#v, want none", audit.InjectedIDs)
	}
}

func TestBrief_ConflictDownrank(t *testing.T) {
	useTempVault(t)
	writeBriefMemory(t, Memory{ID: "mem_winner", Project: "brief-proj", Title: "Winner", Score: 10, Status: "active", Source: "manual", Inject: "query", ConflictsWith: []string{"mem_conflict"}}, "shared query winner")
	writeBriefMemory(t, Memory{ID: "mem_conflict", Project: "brief-proj", Title: "Conflict", Score: 9, Status: "active", Source: "manual", Inject: "query"}, "shared query conflict")

	block, audit, err := Brief("brief-proj", "shared query", Budget{MaxTokens: 4000})
	if err != nil {
		t.Fatalf("Brief() error = %v", err)
	}
	if audit.ConflictsDownrank != 1 {
		t.Fatalf("ConflictsDownrank = %d, want 1; audit=%+v", audit.ConflictsDownrank, audit)
	}
	if got, want := audit.InjectedIDs, []string{"mem_winner", "mem_conflict"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("InjectedIDs = %#v, want %#v", got, want)
	}
	if !strings.Contains(block, "Conflict") {
		t.Fatalf("block = %q, want downranked conflict still injected", block)
	}
}

func TestBrief_InjectedIDsOrder(t *testing.T) {
	useTempVault(t)
	writeBriefMemory(t, Memory{ID: "mem_always", Project: "brief-proj", Title: "Always", Score: 1, Status: "active", Source: "manual", Inject: "always"}, "always body")
	writeBriefMemory(t, Memory{ID: "mem_high", Project: "brief-proj", Title: "High", Score: 10, Status: "active", Source: "manual", Inject: "query"}, "order query high")
	writeBriefMemory(t, Memory{ID: "mem_low", Project: "brief-proj", Title: "Low", Score: 2, Status: "active", Source: "manual", Inject: "query"}, "order query low")

	_, audit, err := Brief("brief-proj", "order query", Budget{MaxTokens: 4000})
	if err != nil {
		t.Fatalf("Brief() error = %v", err)
	}
	if got, want := audit.InjectedIDs, []string{"mem_always", "mem_high", "mem_low"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("InjectedIDs = %#v, want %#v", got, want)
	}
}

func writeBriefMemory(t *testing.T, mem Memory, body string) {
	t.Helper()
	if mem.Type == "" {
		mem.Type = "decision"
	}
	if mem.Tags == nil {
		mem.Tags = []string{"brief"}
	}
	dir := filepath.Join(VaultDir, "notes", mem.Project)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dir, err)
	}
	content := fmt.Sprintf(`---
id: %s
type: %s
project: %s
tags: [%s]
title: %s
score: %g
status: %s
source: %s
inject: %s
`, mem.ID, mem.Type, mem.Project, strings.Join(mem.Tags, ", "), mem.Title, mem.Score, mem.Status, mem.Source, mem.Inject)
	if len(mem.ConflictsWith) > 0 {
		content += "conflicts_with:\n"
		for _, id := range mem.ConflictsWith {
			content += fmt.Sprintf("  - %s\n", id)
		}
	}
	content += "---\n" + body
	if err := os.WriteFile(filepath.Join(dir, mem.ID+".md"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(memory) error = %v", err)
	}
}
