package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ming-agents/server/memory"
)

func TestInjectBrief_WritesAuditJSON(t *testing.T) {
	restore := stubWorkflowBrief(t, "memory body", memory.BriefAudit{InjectedIDs: []string{"mem_audit"}})
	defer restore()

	repoRoot := t.TempDir()
	result, err := InjectBrief(context.Background(), BriefInjectContext{
		RunID:    "run-brief",
		RepoRoot: repoRoot,
		Kind:     NodeKindClarification,
		Query:    "build memory injection",
	})
	if err != nil {
		t.Fatalf("InjectBrief() error = %v", err)
	}
	if result == nil {
		t.Fatal("InjectBrief() result = nil")
	}
	if result.Path != filepath.Join(repoRoot, ".workflow", "runs", "run-brief", "clarification-brief.json") {
		t.Fatalf("Path = %q, want clarification brief path", result.Path)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", result.Path, err)
	}
	var decoded struct {
		Status string            `json:"status"`
		Kind   NodeKind          `json:"kind"`
		Query  string            `json:"query"`
		Audit  memory.BriefAudit `json:"audit"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal audit JSON error = %v", err)
	}
	if decoded.Status != "ok" || decoded.Kind != NodeKindClarification || decoded.Query != "build memory injection" {
		t.Fatalf("decoded audit metadata = %+v", decoded)
	}
	if got := decoded.Audit.InjectedIDs; len(got) != 1 || got[0] != "mem_audit" {
		t.Fatalf("InjectedIDs = %#v, want mem_audit", got)
	}
}

func TestInjectBrief_BudgetByKind(t *testing.T) {
	tests := []struct {
		kind NodeKind
		want int
	}{
		{NodeKindClarification, 4000},
		{NodeKindPlanning, 4000},
		{NodeKindDevelopment, 8000},
		{NodeKindReview, 4000},
		{NodeKindEvaluation, 0},
	}
	for _, tt := range tests {
		if got := briefBudgetForKind(tt.kind); got != tt.want {
			t.Fatalf("briefBudgetForKind(%s) = %d, want %d", tt.kind, got, tt.want)
		}
	}
}

func TestInjectBrief_UsesExplicitProjectOrFallback(t *testing.T) {
	var projects []string
	prev := workflowBrief
	workflowBrief = func(project, query string, budget memory.Budget) (string, memory.BriefAudit, error) {
		projects = append(projects, project)
		return "memory body", memory.BriefAudit{InjectedIDs: []string{"mem_project"}}, nil
	}
	defer func() { workflowBrief = prev }()

	for _, tc := range []struct {
		name    string
		project string
		want    string
	}{
		{name: "fallback", project: "", want: reuseProject},
		{name: "explicit", project: "my-project", want: "my-project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := InjectBrief(context.Background(), BriefInjectContext{
				RunID:    "run-" + tc.name,
				RepoRoot: t.TempDir(),
				Kind:     NodeKindClarification,
				Project:  tc.project,
				Query:    "query",
			}); err != nil {
				t.Fatalf("InjectBrief() error = %v", err)
			}
			if got := projects[len(projects)-1]; got != tc.want {
				t.Fatalf("project = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInjectBrief_AuditFields(t *testing.T) {
	prevVault := memory.VaultDir
	memory.VaultDir = t.TempDir()
	t.Cleanup(func() { memory.VaultDir = prevVault })

	memDir := filepath.Join(memory.VaultDir, "notes", "ming-agents")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := "Use the rollout checklist before changing workflow prompts."
	if err := os.WriteFile(filepath.Join(memDir, "mem_real.md"), []byte(`---
id: mem_real
type: decision
project: ming-agents
tags: [workflow]
title: Workflow prompt checklist
score: 9
status: active
source: manual
inject: always
---
`+body), 0644); err != nil {
		t.Fatalf("WriteFile(memory) error = %v", err)
	}

	result, err := InjectBrief(context.Background(), BriefInjectContext{
		RunID:    "run-real",
		RepoRoot: t.TempDir(),
		Kind:     NodeKindPlanning,
		Query:    "workflow prompts",
	})
	if err != nil {
		t.Fatalf("InjectBrief() error = %v", err)
	}
	if result == nil || result.Audit == nil {
		t.Fatalf("InjectBrief() result/audit = %#v", result)
	}
	if len(result.Audit.InjectedIDs) != 1 || result.Audit.InjectedIDs[0] != "mem_real" {
		t.Fatalf("InjectedIDs = %#v, want mem_real", result.Audit.InjectedIDs)
	}
	if !strings.Contains(result.Markdown, "mem_real") || !strings.Contains(result.Markdown, body) {
		t.Fatalf("Markdown = %q, want memory ID and body", result.Markdown)
	}
}

func TestInjectBrief_NoRepoRoot(t *testing.T) {
	result, err := InjectBrief(context.Background(), BriefInjectContext{
		RunID: "run-no-root",
		Kind:  NodeKindClarification,
		Query: "query",
	})
	if err != nil {
		t.Fatalf("InjectBrief() error = %v", err)
	}
	if result != nil {
		t.Fatalf("InjectBrief() result = %#v, want nil", result)
	}
}

func TestInjectBrief_SoftFailure(t *testing.T) {
	restore := stubWorkflowBriefError(t, errors.New("vault scan failed"))
	defer restore()

	repoRoot := t.TempDir()
	result, err := InjectBrief(context.Background(), BriefInjectContext{
		RunID:    "run-soft-fail",
		RepoRoot: repoRoot,
		Kind:     NodeKindReview,
		Query:    "review output",
	})
	if err != nil {
		t.Fatalf("InjectBrief() error = %v", err)
	}
	if result == nil || result.Audit == nil {
		t.Fatalf("InjectBrief() result/audit = %#v", result)
	}
	if result.Markdown != "" {
		t.Fatalf("Markdown = %q, want empty on soft failure", result.Markdown)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", result.Path, err)
	}
	var decoded struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed audit error = %v", err)
	}
	if decoded.Status != "failed" || !strings.Contains(decoded.Error, "vault scan failed") {
		t.Fatalf("decoded failure audit = %+v", decoded)
	}
}

func stubWorkflowBrief(t *testing.T, markdown string, audit memory.BriefAudit) func() {
	t.Helper()
	prev := workflowBrief
	workflowBrief = func(project, query string, budget memory.Budget) (string, memory.BriefAudit, error) {
		return markdown, audit, nil
	}
	return func() { workflowBrief = prev }
}

func stubWorkflowBriefError(t *testing.T, err error) func() {
	t.Helper()
	prev := workflowBrief
	workflowBrief = func(project, query string, budget memory.Budget) (string, memory.BriefAudit, error) {
		return "", memory.BriefAudit{}, err
	}
	return func() { workflowBrief = prev }
}
