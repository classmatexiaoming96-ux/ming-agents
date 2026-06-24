package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ming-agents/server/adapter"
)

type clarificationContext struct {
	RunID             string
	AgentSections     map[string]string
	AggregatedSection string
	Raw               string
}

func RunPlanning(ctx context.Context, repoRoot, clarFile string) (*Plan, error) {
	data, err := os.ReadFile(clarFile)
	if err != nil {
		return nil, fmt.Errorf("read clarification file: %w", err)
	}
	clar := parseClarificationMarkdown(string(data))
	if len(clar.AgentSections) == 0 {
		return nil, fmt.Errorf("no tagged agent sections found in %s", clarFile)
	}

	prompt := renderPlanningPrompt(clar)
	out, err := runCodexPrompt(ctx, repoRoot, prompt, 30*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("run planning prompt: %w", err)
	}
	plan, err := extractPlanJSON(out)
	if err != nil {
		return nil, err
	}
	if err := validatePlan(plan); err != nil {
		return nil, err
	}

	target := filepath.Join(repoRoot, "docs", "planning.md")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return nil, err
	}
	if err := writeTextAtomic(target, renderPlanningMarkdown(clar, clarFile, out, plan)); err != nil {
		return nil, fmt.Errorf("write planning file: %w", err)
	}
	if clar.RunID != "" {
		_ = writeWorkflowState(repoRoot, clar.RunID, map[string]NodeStatus{
			"node1": NodeCompleted,
			"node2": NodeCompleted,
			"node3": NodePending,
		}, map[string]any{"planning_file": target})
	}
	return plan, nil
}

func validatePlan(plan *Plan) error {
	if plan == nil {
		return errors.New("plan is required")
	}
	if strings.TrimSpace(plan.TaskID) == "" {
		return errors.New("task_id is required")
	}
	if len(plan.Subtasks) == 0 {
		return errors.New("subtasks must contain at least one item")
	}
	seen := make(map[string]bool, len(plan.Subtasks))
	for i, st := range plan.Subtasks {
		if strings.TrimSpace(st.ID) == "" {
			return fmt.Errorf("subtask at index %d missing id", i)
		}
		if seen[st.ID] {
			return fmt.Errorf("duplicate subtask id %q", st.ID)
		}
		seen[st.ID] = true
		if st.AgentType != "codex" {
			return fmt.Errorf("unsupported agent_type for %s: %q", st.ID, st.AgentType)
		}
		if !validRepoPath(st.RepoPath) {
			return fmt.Errorf("invalid repo_path for %s: %q", st.ID, st.RepoPath)
		}
		if strings.TrimSpace(st.Description) == "" {
			return fmt.Errorf("subtask %s missing description", st.ID)
		}
		if len(st.AcceptanceCriteria) == 0 {
			return fmt.Errorf("subtask %s missing acceptance criteria", st.ID)
		}
		for j, criterion := range st.AcceptanceCriteria {
			if strings.TrimSpace(criterion) == "" {
				return fmt.Errorf("subtask %s acceptance criteria %d is empty", st.ID, j)
			}
		}
	}
	return nil
}

func validRepoPath(repoPath string) bool {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" || filepath.IsAbs(repoPath) {
		return false
	}
	clean := filepath.Clean(repoPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == ".." {
			return false
		}
	}
	return true
}

func parseClarificationMarkdown(markdown string) clarificationContext {
	clar := clarificationContext{
		Raw:           markdown,
		AgentSections: map[string]string{},
		RunID:         extractMetadataLine(markdown, "run_id"),
	}
	re := regexp.MustCompile(`(?s)<!-- agent:([^ ]+) begin -->(.*?)<!-- agent:\1 end -->`)
	for _, match := range re.FindAllStringSubmatch(markdown, -1) {
		clar.AgentSections[strings.TrimSpace(match[1])] = strings.TrimSpace(match[2])
	}
	if idx := strings.Index(markdown, "## Aggregated Clarifications"); idx >= 0 {
		clar.AggregatedSection = strings.TrimSpace(markdown[idx:])
	}
	return clar
}

func extractMetadataLine(markdown, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func renderPlanningPrompt(clar clarificationContext) string {
	var b strings.Builder
	b.WriteString("# Role\n")
	b.WriteString("You are the planning agent for a dynamic workflow run.\n\n")
	b.WriteString("# Source\n")
	b.WriteString("docs/requirements-clarity.md\n\n")
	b.WriteString("# Clarification Context\n")
	for agent, section := range clar.AgentSections {
		fmt.Fprintf(&b, "## Agent: %s\n%s\n\n", agent, section)
	}
	if clar.AggregatedSection != "" {
		b.WriteString(clar.AggregatedSection)
		b.WriteString("\n\n")
	}
	b.WriteString("# Task\n")
	b.WriteString("Create a concrete execution plan. Do not modify files other than returning the planning document.\n\n")
	b.WriteString("# Output Format\n")
	b.WriteString("Return markdown with a human-readable planning summary followed by exactly one fenced JSON block under '## Execution Plan JSON'.\n")
	b.WriteString("The JSON must match this schema:\n")
	b.WriteString("```json\n")
	b.WriteString(`{"task_id":"stable-task-id","subtasks":[{"id":"subtask-1","agent_type":"codex","repo_path":"relative/path","description":"concrete work","acceptance_criteria":["observable criterion"]}]}`)
	b.WriteString("\n```\n")
	b.WriteString("All subtasks must use agent_type codex and repo_path values relative to the repository root without '..'.\n")
	return b.String()
}

func extractPlanJSON(markdown string) (*Plan, error) {
	re := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")
	match := re.FindStringSubmatch(markdown)
	if len(match) != 2 {
		return nil, errors.New("planning output missing fenced json plan")
	}
	var plan Plan
	if err := json.Unmarshal([]byte(match[1]), &plan); err != nil {
		return nil, fmt.Errorf("parse planning json: %w", err)
	}
	return &plan, nil
}

func renderPlanningMarkdown(clar clarificationContext, clarFile, plannerOut string, plan *Plan) string {
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	runID := clar.RunID
	if runID == "" {
		runID = plan.TaskID
	}
	var b strings.Builder
	b.WriteString("# Planning\n\n")
	fmt.Fprintf(&b, "run_id: %s\nnode: 2\nstate: COMPLETED\nsource: %s\n\n", runID, filepath.ToSlash(clarFile))
	b.WriteString("## Planning Summary\n\n")
	summary := extractSection(plannerOut, "Planning Summary")
	if strings.TrimSpace(summary) == "" {
		b.WriteString("- Goal: execute the clarified user request through validated subtasks.\n")
		b.WriteString("- Execution strategy: dispatch each validated subtask to a Codex development session.\n")
		b.WriteString("- Review strategy: run a review barrier after development and retry blocking subtasks once.\n")
	} else {
		b.WriteString(strings.TrimSpace(summary))
		b.WriteString("\n")
	}
	b.WriteString("\n## Execution Plan JSON\n\n```json\n")
	b.Write(planJSON)
	b.WriteString("\n```\n")
	return b.String()
}

func extractSection(markdown, heading string) string {
	lines := strings.Split(markdown, "\n")
	target := "## " + heading
	inSection := false
	var out []string
	for _, line := range lines {
		if strings.TrimSpace(line) == target {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(strings.TrimSpace(line), "## ") {
			break
		}
		if inSection {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func runCodexPrompt(ctx context.Context, repoRoot, prompt string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	mgr := adapter.NewCodexSessionManager(adapter.CodexConfig{Command: "codex"})
	sess, err := mgr.StartSession(runCtx, repoRoot)
	if err != nil {
		return "", err
	}
	defer sess.Close()
	return sess.SendPrompt(runCtx, prompt)
}

func writeWorkflowState(repoRoot, runID string, nodes map[string]NodeStatus, details map[string]any) error {
	state := RunState{RunID: runID, Nodes: nodes, Details: details}
	return writeJSONAtomic(filepath.Join(repoRoot, ".workflow", "runs", runID, "state.json"), state)
}
