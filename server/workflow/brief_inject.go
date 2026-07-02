package workflow

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ming-agents/server/memory"
)

var workflowBrief = memory.Brief

// workflowImplicitFeedback is indirected so tests can observe the feedback loop
// without touching the real vault. It defaults to the memory package entry
// point, which mutates memory files (score / pending_score) for the injected
// memories that the LLM turn actually referenced.
var workflowImplicitFeedback = memory.ImplicitFeedback

// implicitFeedbackDisabled reports whether the workflow-level implicit feedback
// loop is turned off. It is the dry-run guard from the design: because
// ImplicitFeedback mutates memory files, an operator can disable the online
// score adjustment by setting WORKFLOW_IMPLICIT_FEEDBACK_DISABLED.
func implicitFeedbackDisabled() bool {
	return strings.TrimSpace(os.Getenv("WORKFLOW_IMPLICIT_FEEDBACK_DISABLED")) != ""
}

// applyImplicitFeedback closes the brief -> output -> score loop: it feeds the
// memories that a node injected (brief.Audit.InjectedIDs) and the assistant
// output of that LLM turn into ImplicitFeedback, so referenced memories gain
// pending_score and contradicted ones are penalised. It is deliberately
// soft-fail (log only) so a feedback error never blocks the workflow, and a
// no-op when there is nothing to score.
func applyImplicitFeedback(brief *BriefInjectResult, output string) {
	if implicitFeedbackDisabled() {
		return
	}
	if brief == nil || brief.Audit == nil || len(brief.Audit.InjectedIDs) == 0 {
		return
	}
	if strings.TrimSpace(output) == "" {
		return
	}
	if _, err := workflowImplicitFeedback(brief.Audit.InjectedIDs, output); err != nil {
		log.Printf("implicit feedback failed for %v: %v", brief.Audit.InjectedIDs, err)
	}
}

type BriefInjectContext struct {
	RunID     string
	RepoRoot  string
	Kind      NodeKind
	Project   string
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
	Project     string            `json:"project"`
	Query       string            `json:"query"`
	Budget      int               `json:"budget"`
	GeneratedAt string            `json:"generated_at"`
	Audit       memory.BriefAudit `json:"audit"`
}

// InjectBrief injects project-scoped memory for one workflow node and writes a
// *-brief.json audit next to the run artifacts.
//
// Return shapes are part of the workflow contract:
//   - (nil, nil): no-op because RepoRoot or RunID is empty.
//   - (*BriefInjectResult, nil) with status "failed": soft failure; memory
//     lookup failed but the node must continue without a brief block.
//   - (*BriefInjectResult, nil) with status "ok": memory was queried and audit
//     was written; Markdown may still be empty when no memories were injected.
func InjectBrief(ctx context.Context, ic BriefInjectContext) (*BriefInjectResult, error) {
	if ic.RepoRoot == "" || ic.RunID == "" {
		return nil, nil
	}
	budget := briefBudgetForKind(ic.Kind)
	path := briefAuditPath(ic)
	audit := memory.BriefAudit{}
	project := briefProject(ic.Project)
	record := briefAuditFile{
		Status:      "ok",
		RunID:       ic.RunID,
		Kind:        ic.Kind,
		Project:     project,
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

	block, gotAudit, err := workflowBrief(project, ic.Query, memory.Budget{MaxTokens: budget})
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

func briefProject(project string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		return reuseProject
	}
	return project
}

func projectFromRepoRoot(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return ""
	}
	base := filepath.Base(filepath.Clean(repoRoot))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
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
