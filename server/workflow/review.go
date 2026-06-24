package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	case "description":
		issue.Description = value
	case "required_fix", "required fixes", "required_fixes":
		if value != "" {
			issue.RequiredFixes = append(issue.RequiredFixes, value)
		}
	}
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
		if strings.TrimSpace(result.Output) != "" {
			b.WriteString("```text\n")
			b.WriteString(result.Output)
			if !strings.HasSuffix(result.Output, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
	}
	b.WriteString("# Diff Snapshot\n")
	b.WriteString("Review the repository diff at: ")
	b.WriteString(diffFile)
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
