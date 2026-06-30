package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type ReviewAttemptPaths struct {
	PromptFile  string
	OutFile     string
	ExitFile    string
	HistoryFile string
}

type ReviewSubtaskPaths struct {
	SubtaskID     string
	SafeSubtaskID string
	SessionID     string
	Dir           string
	ReviewAttemptPaths
}

func NewReviewSubtaskPaths(repoRoot, runID, subtaskID string) ReviewSubtaskPaths {
	safeID := safeReviewSubtaskID(subtaskID)
	dir := filepath.Join(repoRoot, ".workflow", "runs", runID, "review", "subtasks", safeID)
	return ReviewSubtaskPaths{
		SubtaskID:     subtaskID,
		SafeSubtaskID: safeID,
		SessionID:     NewPTYSessionID(runID, "review", "subtask-"+subtaskID, 1),
		Dir:           dir,
		ReviewAttemptPaths: ReviewAttemptPaths{
			PromptFile:  filepath.Join(dir, "review-"+safeID+".prompt.md"),
			OutFile:     filepath.Join(dir, "review-"+safeID+".out.md"),
			ExitFile:    filepath.Join(dir, "review-"+safeID+".exit"),
			HistoryFile: filepath.Join(dir, "review-"+safeID+".messages.jsonl"),
		},
	}
}

func safeReviewSubtaskID(subtaskID string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range subtaskID {
		safe := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
		if safe {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	safe := strings.Trim(b.String(), ".-")
	for strings.Contains(safe, "..") {
		safe = strings.ReplaceAll(safe, "..", ".")
	}
	if safe == "" {
		return "subtask"
	}
	return safe
}

func ParseReviewReport(markdown string) *ReviewReport {
	report := &ReviewReport{Passed: true}
	section := ""
	var current *ReviewIssue
	inRequiredFixes := false

	flushIssue := func() {
		if current == nil {
			return
		}
		current.Severity = normalizeSeverity(current.Severity)
		if current.Severity == "blocking" {
			report.Passed = false
		}
		report.Issues = append(report.Issues, *current)
		current = nil
		inRequiredFixes = false
	}

	lines := strings.Split(markdown, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "## ") {
			flushIssue()
			section = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			continue
		}
		switch section {
		case "summary":
			if line != "" {
				if report.Summary != "" {
					report.Summary += "\n"
				}
				report.Summary += strings.TrimPrefix(line, "- ")
			}
		case "issues":
			if strings.HasPrefix(line, "- ") {
				if strings.Contains(strings.ToLower(line), "severity:") {
					flushIssue()
					current = &ReviewIssue{}
					parseReviewIssueField(current, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
					inRequiredFixes = false
				} else if current != nil && inRequiredFixes {
					current.RequiredFixes = append(current.RequiredFixes, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
				} else if current != nil {
					if current.Description != "" {
						current.Description += "\n"
					}
					current.Description += strings.TrimSpace(strings.TrimPrefix(line, "- "))
				}
				continue
			}
			if current == nil || line == "" {
				continue
			}
			if strings.HasPrefix(line, "- ") {
				current.RequiredFixes = append(current.RequiredFixes, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
				continue
			}
			if strings.HasPrefix(line, "required_fixes:") || strings.HasPrefix(line, "Required fixes:") {
				inRequiredFixes = true
				continue
			}
			if strings.HasPrefix(line, "-") {
				current.RequiredFixes = append(current.RequiredFixes, strings.TrimSpace(strings.TrimPrefix(line, "-")))
				continue
			}
			inRequiredFixes = false
			parseReviewIssueField(current, line)
		}
	}
	flushIssue()
	if len(report.Issues) == 0 && strings.Contains(strings.ToLower(markdown), "blocking") {
		report.Passed = false
	}
	return report
}

func parseReviewIssueField(issue *ReviewIssue, line string) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		if issue.Description != "" {
			issue.Description += "\n"
		}
		issue.Description += strings.TrimSpace(line)
		return
	}
	value = strings.TrimSpace(value)
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "severity":
		issue.Severity = value
	case "subtask_id", "subtask":
		issue.SubtaskID = value
	case "session_id", "session":
		issue.SessionID = value
	case "failure_class", "failure class":
		issue.FailureClass = FailureClass(value)
	case "evidence_refs", "evidence refs", "evidence":
		issue.EvidenceRefs = splitReviewCSV(value)
	case "description":
		issue.Description = value
	case "required_fix", "required fixes", "required_fixes":
		if value != "" {
			issue.RequiredFixes = append(issue.RequiredFixes, value)
		}
	}
}

func splitReviewCSV(value string) []string {
	var refs []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			refs = append(refs, part)
		}
	}
	return refs
}

func normalizeSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "blocking", "blocker", "error", "critical":
		return "blocking"
	case "warning", "warn":
		return "warning"
	case "info", "note":
		return "info"
	default:
		if strings.TrimSpace(severity) == "" {
			return "warning"
		}
		return strings.ToLower(strings.TrimSpace(severity))
	}
}

func renderReviewPrompt(plan *Plan, results []*SubtaskResult, diffFile string) string {
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	var b strings.Builder
	b.WriteString("# Role\n")
	b.WriteString("You are the review agent for a dynamic workflow run.\n\n")
	b.WriteString("# Plan\n")
	b.Write(planJSON)
	b.WriteString("\n\n# Development Results\n")
	for _, result := range results {
		if result == nil {
			continue
		}
		fmt.Fprintf(&b, "## %s\n", result.Subtask.ID)
		fmt.Fprintf(&b, "session_id: %s\nstatus: %s\nexit_code: %d\nout_file: %s\n\n", result.SessionID, result.Status, result.ExitCode, result.OutFile)
		if len(result.Subtask.AcceptanceCriteria) > 0 {
			b.WriteString("acceptance_criteria:\n")
			for _, criterion := range result.Subtask.AcceptanceCriteria {
				fmt.Fprintf(&b, "- %s\n", criterion)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("# Diff Snapshot\n")
	b.WriteString("Review the repository diff at: ")
	b.WriteString(diffFile)
	b.WriteString("\n\n# Artifact Review Instructions\n")
	b.WriteString("Read the artifact file paths above and the diff snapshot. Do not rely on development session history or embedded development output.\n")
	b.WriteString("The expected final output document is docs/output.md.\n")
	b.WriteString("\n\n# Review Checklist\n")
	b.WriteString("- Plan coverage: every subtask acceptance criterion is satisfied or explicitly marked blocked.\n")
	b.WriteString("- Scope control: changes are limited to planned repo paths unless justified.\n")
	b.WriteString("- Build and tests: relevant tests are added or updated and verification commands are reported.\n")
	b.WriteString("- Regression risk: shared interfaces, migrations, adapters, and workflow contracts remain compatible.\n")
	b.WriteString("- Error handling: failure paths are represented in code and surfaced in user-readable output.\n")
	b.WriteString("- File contracts: docs/requirements-clarity.md, docs/planning.md, and docs/output.md remain parseable markdown contracts.\n")
	b.WriteString("- Security and safety: prompts do not leak secrets, shell commands quote user-controlled input, and repo paths cannot escape the repo root.\n")
	b.WriteString("- Operational recovery: failed subtasks include enough session, output, and exit information for retry.\n\n")
	b.WriteString("# Output Format\n")
	b.WriteString("Return markdown with these exact headings:\n\n")
	b.WriteString("## Summary\n")
	b.WriteString("One concise review summary.\n\n")
	b.WriteString("## Issues\n")
	b.WriteString("For each issue use:\n")
	b.WriteString("- severity: info|warning|blocking\n")
	b.WriteString("  subtask_id: optional subtask id\n")
	b.WriteString("  session_id: optional session id\n")
	b.WriteString("  description: concrete issue\n")
	b.WriteString("  required_fixes:\n")
	b.WriteString("  - concrete fix\n")
	return b.String()
}

func renderSubtaskReviewPrompt(plan *Plan, result *SubtaskResult, diffFile string) string {
	var subtask Subtask
	sessionID := ""
	status := ""
	exitCode := 0
	outFile := ""
	if result != nil {
		subtask = result.Subtask
		sessionID = result.SessionID
		status = result.Status
		exitCode = result.ExitCode
		outFile = result.OutFile
	}
	plannedFiles := append([]string(nil), subtask.PlannedFiles...)
	sort.Strings(plannedFiles)

	var b strings.Builder
	b.WriteString("# Role\n")
	b.WriteString("You are the review agent for exactly one development subtask.\n\n")
	b.WriteString("# Run\n")
	if plan != nil {
		fmt.Fprintf(&b, "task_id: %s\n", plan.TaskID)
	}
	b.WriteString("\n# Target Subtask\n")
	fmt.Fprintf(&b, "subtask_id: %s\n", subtask.ID)
	fmt.Fprintf(&b, "session_id: %s\n", sessionID)
	fmt.Fprintf(&b, "repo_path: %s\n", subtask.RepoPath)
	fmt.Fprintf(&b, "description: %s\n", subtask.Description)
	fmt.Fprintf(&b, "status: %s\n", status)
	fmt.Fprintf(&b, "exit_code: %d\n", exitCode)
	fmt.Fprintf(&b, "out_file: %s\n\n", outFile)
	if len(plannedFiles) > 0 {
		b.WriteString("planned_files:\n")
		for _, file := range plannedFiles {
			fmt.Fprintf(&b, "- %s\n", file)
		}
		b.WriteString("\n")
	}
	if len(subtask.AcceptanceCriteria) > 0 {
		b.WriteString("acceptance_criteria:\n")
		for _, criterion := range subtask.AcceptanceCriteria {
			fmt.Fprintf(&b, "- %s\n", criterion)
		}
		b.WriteString("\n")
	}
	b.WriteString("# Diff Snapshot\n")
	b.WriteString("Review the repository diff at: ")
	b.WriteString(diffFile)
	b.WriteString("\n\n")
	b.WriteString("# Review Instructions\n")
	b.WriteString("Focus only on the target subtask, its planned files, repo path, acceptance criteria, and referenced artifacts. Do not review unrelated subtasks.\n")
	b.WriteString("Read artifact files by path; do not rely on embedded development output.\n\n")
	b.WriteString("# Output Format\n")
	b.WriteString("Return markdown with these exact headings:\n\n")
	b.WriteString("## Summary\n")
	b.WriteString("One concise review summary for this subtask.\n\n")
	b.WriteString("## Issues\n")
	b.WriteString("For each issue use:\n")
	b.WriteString("- severity: info|warning|blocking\n")
	b.WriteString("  subtask_id: ")
	b.WriteString(subtask.ID)
	b.WriteString("\n")
	b.WriteString("  session_id: ")
	b.WriteString(sessionID)
	b.WriteString("\n")
	b.WriteString("  description: concrete issue\n")
	b.WriteString("  required_fixes:\n")
	b.WriteString("  - concrete fix\n")
	return b.String()
}

func writeReviewInputs(repoRoot, nodeDir string, plan *Plan, results []*SubtaskResult) (string, string, error) {
	diffFile := filepath.Join(nodeDir, "review.diff")
	statusFile := filepath.Join(nodeDir, "review.status")
	if err := writeCommandOutput(repoRoot, diffFile, "git", "diff", "--", ".", ":!docs/output.md"); err != nil {
		return "", "", err
	}
	if err := writeCommandOutput(repoRoot, statusFile, "git", "status", "--short"); err != nil {
		return "", "", err
	}
	_ = plan
	_ = results
	return diffFile, statusFile, nil
}

func writeCommandOutput(workDir, target, name string, args ...string) error {
	data, err := runCommandOutput(workDir, name, args...)
	if err != nil {
		data = append(data, []byte("\ncommand error: "+err.Error()+"\n")...)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0644)
}

func runCommandOutput(workDir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = workDir
	return cmd.CombinedOutput()
}
