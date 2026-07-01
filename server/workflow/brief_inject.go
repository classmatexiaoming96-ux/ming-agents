package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ming-agents/server/memory"
)

var workflowBrief = memory.Brief

type BriefInjectContext struct {
	RunID     string
	RepoRoot  string
	Kind      NodeKind
	Query     string
	AuditName string
}

type BriefInjectResult struct {
	Markdown string
	Audit    *memory.BriefAudit
	Path     string
}

type briefAuditFile struct {
	Status      string            `json:"status"`
	Error       string            `json:"error,omitempty"`
	RunID       string            `json:"run_id"`
	Kind        NodeKind          `json:"kind"`
	Query       string            `json:"query"`
	Budget      int               `json:"budget"`
	GeneratedAt string            `json:"generated_at"`
	Audit       memory.BriefAudit `json:"audit"`
}

func InjectBrief(ctx context.Context, ic BriefInjectContext) (*BriefInjectResult, error) {
	if ic.RepoRoot == "" || ic.RunID == "" {
		return nil, nil
	}
	budget := briefBudgetForKind(ic.Kind)
	path := briefAuditPath(ic)
	audit := memory.BriefAudit{}
	record := briefAuditFile{
		Status:      "ok",
		RunID:       ic.RunID,
		Kind:        ic.Kind,
		Query:       ic.Query,
		Budget:      budget,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Audit:       audit,
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	if budget == 0 {
		record.Status = "skipped"
		if err := writeJSONAtomic(path, record); err != nil {
			return nil, err
		}
		return &BriefInjectResult{Audit: &audit, Path: path}, nil
	}

	select {
	case <-ctx.Done():
		record.Status = "failed"
		record.Error = ctx.Err().Error()
		_ = writeJSONAtomic(path, record)
		return &BriefInjectResult{Audit: &audit, Path: path}, nil
	default:
	}

	block, gotAudit, err := workflowBrief(reuseProject, ic.Query, memory.Budget{MaxTokens: budget})
	if err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		if writeErr := writeJSONAtomic(path, record); writeErr != nil {
			return nil, writeErr
		}
		return &BriefInjectResult{Audit: &audit, Path: path}, nil
	}

	record.Audit = gotAudit
	markdown := formatBriefMarkdown(block, gotAudit)
	if err := writeJSONAtomic(path, record); err != nil {
		return nil, err
	}
	return &BriefInjectResult{Markdown: markdown, Audit: &gotAudit, Path: path}, nil
}

func briefBudgetForKind(kind NodeKind) int {
	switch kind {
	case NodeKindClarification, NodeKindPlanning, NodeKindReview:
		return 4000
	case NodeKindDevelopment:
		return 8000
	case NodeKindEvaluation:
		return 0
	default:
		return 4000
	}
}

func briefAuditPath(ic BriefInjectContext) string {
	name := string(ic.Kind)
	if ic.AuditName != "" {
		name = safeScope(ic.AuditName)
	}
	return filepath.Join(ic.RepoRoot, ".workflow", "runs", ic.RunID, name+"-brief.json")
}

func formatBriefMarkdown(block string, audit memory.BriefAudit) string {
	block = strings.TrimSpace(block)
	if block == "" || len(audit.InjectedIDs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Relevant Memory\n\n")
	b.WriteString("Injected memory IDs:\n")
	for _, id := range audit.InjectedIDs {
		fmt.Fprintf(&b, "- %s\n", id)
	}
	b.WriteString("\n")
	b.WriteString(block)
	b.WriteString("\n\n")
	return b.String()
}

func prependRelevantMemory(memoryBlock, prompt string) string {
	memoryBlock = strings.TrimSpace(memoryBlock)
	if memoryBlock == "" {
		return prompt
	}
	return memoryBlock + "\n" + prompt
}

func developmentBriefQuery(st Subtask) string {
	var b strings.Builder
	b.WriteString(st.Description)
	for _, criterion := range st.AcceptanceCriteria {
		if strings.TrimSpace(criterion) == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(criterion)
	}
	return b.String()
}

func reviewBriefQuery(plan *Plan, results []*SubtaskResult) string {
	var b strings.Builder
	if plan != nil {
		for _, st := range plan.Subtasks {
			fmt.Fprintf(&b, "%s\n", st.Description)
			for _, criterion := range st.AcceptanceCriteria {
				fmt.Fprintf(&b, "%s\n", criterion)
			}
		}
	}
	for _, result := range results {
		if result == nil {
			continue
		}
		fmt.Fprintf(&b, "%s\n%s\n", result.Subtask.Description, result.Output)
	}
	return b.String()
}

func briefMarkdown(result *BriefInjectResult) string {
	if result == nil {
		return ""
	}
	return result.Markdown
}

func nodeResultWithBrief(result *NodeResult, brief *BriefInjectResult) *NodeResult {
	if result == nil || brief == nil {
		return result
	}
	result.BriefAudit = brief.Audit
	result.BriefPath = brief.Path
	if brief.Path != "" {
		result.OutputPaths = append(result.OutputPaths, brief.Path)
	}
	return result
}

func firstBriefResult(briefs map[string]*BriefInjectResult, subtasks []Subtask) *BriefInjectResult {
	for _, st := range subtasks {
		if brief := briefs[st.ID]; brief != nil {
			return brief
		}
	}
	return nil
}
