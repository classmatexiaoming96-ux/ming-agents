package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type reviewCodexPromptRunner func(context.Context, string, string, time.Duration) (string, error)

var runReviewCodexPrompt reviewCodexPromptRunner = runCodexPrompt

type reviewContractError struct {
	reason string
}

func (e *reviewContractError) Error() string {
	return e.reason
}

func (e *reviewContractError) FailureClass() FailureClass {
	return FailureClassContractError
}

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

type ReviewAggregatePaths struct {
	SessionID string
	Dir       string
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

func NewReviewAggregatePaths(repoRoot, runID string) ReviewAggregatePaths {
	dir := filepath.Join(repoRoot, ".workflow", "runs", runID, "review", "aggregate")
	return ReviewAggregatePaths{
		SessionID: NewPTYSessionID(runID, "review", "aggregate", 1),
		Dir:       dir,
		ReviewAttemptPaths: ReviewAttemptPaths{
			PromptFile:  filepath.Join(dir, "review-aggregate.prompt.md"),
			OutFile:     filepath.Join(dir, "review-aggregate.out.md"),
			ExitFile:    filepath.Join(dir, "review-aggregate.exit"),
			HistoryFile: filepath.Join(dir, "review-aggregate.messages.jsonl"),
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
				_, value, _ := strings.Cut(line, ":")
				if value = strings.TrimSpace(value); value != "" {
					current.RequiredFixes = append(current.RequiredFixes, value)
				}
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

func ValidateReviewReport(report *ReviewReport, results []*SubtaskResult, expectedSubtaskID string) error {
	if report == nil {
		return &reviewContractError{reason: "review report is nil"}
	}
	sessionsBySubtask := map[string]string{}
	for _, result := range results {
		if result == nil {
			continue
		}
		sessionsBySubtask[result.Subtask.ID] = result.SessionID
	}
	for _, issue := range report.Issues {
		if normalizeSeverity(issue.Severity) != "blocking" {
			continue
		}
		if strings.TrimSpace(issue.SubtaskID) == "" {
			return &reviewContractError{reason: "blocking review issue missing subtask_id"}
		}
		if expectedSubtaskID != "" && issue.SubtaskID != expectedSubtaskID {
			return &reviewContractError{reason: "blocking review issue points at unexpected subtask_id"}
		}
		if strings.TrimSpace(issue.SessionID) == "" {
			return &reviewContractError{reason: "blocking review issue missing session_id"}
		}
		if wantSession := sessionsBySubtask[issue.SubtaskID]; wantSession != "" && issue.SessionID != wantSession {
			return &reviewContractError{reason: "blocking review issue points at unexpected session_id"}
		}
		if strings.TrimSpace(issue.Description) == "" {
			return &reviewContractError{reason: "blocking review issue missing description"}
		}
		if len(issue.RequiredFixes) == 0 {
			return &reviewContractError{reason: "blocking review issue missing required_fixes"}
		}
	}
	return nil
}

func renderReviewContractRevisionPrompt(originalPrompt, output string, err error) string {
	var b strings.Builder
	b.WriteString("# Review Report Contract Revision\n")
	b.WriteString("Fix only the review report contract. Do not request code changes and do not inspect or modify repository files.\n\n")
	b.WriteString("# Contract Error\n")
	b.WriteString(err.Error())
	b.WriteString("\n\n# Previous Review Prompt\n")
	b.WriteString(originalPrompt)
	b.WriteString("\n\n# Previous Invalid Review Output\n")
	b.WriteString(output)
	b.WriteString("\n\n# Required Output\n")
	b.WriteString("Return the same review result with valid fields. Every blocking issue must include subtask_id, session_id, description, and required_fixes.\n")
	return b.String()
}

func renderReviewHumanRejectRevisionPrompt(originalPrompt, output string, decision ReviewDecision) string {
	var b strings.Builder
	b.WriteString("# Review Human Rejection Revision\n")
	b.WriteString("Revise only the review report in response to the human rejection. Do not inspect or modify repository files.\n\n")
	b.WriteString("# Rejection Reason\n")
	b.WriteString(decision.Reason)
	b.WriteString("\n\n# Previous Review Prompt\n")
	b.WriteString(originalPrompt)
	b.WriteString("\n\n# Previous Review Output\n")
	b.WriteString(output)
	b.WriteString("\n\n# Required Output\n")
	b.WriteString("Return a corrected review report using the required review output format.\n")
	return b.String()
}

func renderAggregateReviewPrompt(plan *Plan, subtaskReports map[string]*ReviewReport) string {
	var ids []string
	for id := range subtaskReports {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	b.WriteString("# Aggregate Review\n")
	b.WriteString("Review cross-subtask consistency, shared files, documentation alignment, and integration risk across completed subtask review reports.\n\n")
	if plan != nil {
		fmt.Fprintf(&b, "task_id: %s\n\n", plan.TaskID)
	}
	b.WriteString("# Subtask Review Summaries\n")
	for _, id := range ids {
		report := subtaskReports[id]
		fmt.Fprintf(&b, "## %s\n", id)
		if report == nil {
			b.WriteString("summary: missing report\n\n")
			continue
		}
		fmt.Fprintf(&b, "passed: %t\n", report.Passed)
		fmt.Fprintf(&b, "summary: %s\n", report.Summary)
		if len(report.Issues) > 0 {
			b.WriteString("issues:\n")
			for _, issue := range report.Issues {
				fmt.Fprintf(&b, "- %s: %s\n", normalizeSeverity(issue.Severity), issue.Description)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("# Output Format\n")
	b.WriteString("Return markdown with these exact headings:\n\n")
	b.WriteString("## Summary\n")
	b.WriteString("One concise aggregate review summary.\n\n")
	b.WriteString("## Issues\n")
	b.WriteString("For each issue use:\n")
	b.WriteString("- severity: info|warning|blocking\n")
	b.WriteString("  description: concrete cross-subtask issue\n")
	b.WriteString("  required_fixes:\n")
	b.WriteString("  - concrete fix\n")
	return b.String()
}

func RunSubtaskReview(ctx context.Context, repoRoot, runID string, plan *Plan, result *SubtaskResult, diffFile string) (*ReviewReport, string, ReviewSubtaskPaths, error) {
	if result == nil {
		return nil, "", ReviewSubtaskPaths{}, fmt.Errorf("subtask result is nil")
	}
	paths := NewReviewSubtaskPaths(repoRoot, runID, result.Subtask.ID)
	if err := os.MkdirAll(paths.Dir, 0755); err != nil {
		return nil, "", paths, err
	}
	session := AgentSession{
		ID:          paths.SessionID,
		AgentType:   "review",
		Status:      AgentSessionRunning,
		HistoryFile: paths.HistoryFile,
	}
	RegisterAgentSession(session)

	prompt := renderSubtaskReviewPrompt(plan, result, diffFile)
	if err := writeTextAtomic(paths.PromptFile, prompt); err != nil {
		return nil, "", paths, err
	}
	agent := &SubtaskAgent{SubtaskID: result.Subtask.ID, Session: session}
	_ = AppendAgentMessage(agent, AgentMessage{Role: "system", Content: prompt})
	_ = appendReviewApprovalRequest(agent, "review:subtask:"+result.Subtask.ID)

	output, err := runReviewCodexPrompt(ctx, repoRoot, prompt, 30*time.Minute)
	exitCode := 0
	if err != nil {
		exitCode = 1
		output = err.Error()
	}
	_ = os.WriteFile(paths.OutFile, []byte(output), 0644)
	_ = os.WriteFile(paths.ExitFile, []byte(fmt.Sprintf("%d\n", exitCode)), 0644)
	_ = AppendAgentMessage(agent, AgentMessage{Role: "assistant", Content: output})

	report := ParseReviewReport(output)
	if err != nil {
		report.Passed = false
		report.Issues = append(report.Issues, ReviewIssue{
			SubtaskID:   result.Subtask.ID,
			SessionID:   paths.SessionID,
			Severity:    "blocking",
			Description: err.Error(),
		})
	}
	if err == nil {
		if contractErr := ValidateReviewReport(report, []*SubtaskResult{result}, result.Subtask.ID); contractErr != nil {
			_ = writeReviewAttempt(repoRoot, runID, "review:subtask:"+result.Subtask.ID, paths.SessionID, result.Subtask.ID, 0, -1, "contract_error", FailureClassContractError, contractErr.Error(), paths.ReviewAttemptPaths)
			revisionPromptPath, revisionOutPath, revisionExitPath := AttemptPathsForRevision(paths.PromptFile, paths.OutFile, paths.ExitFile, 1)
			revisionPaths := ReviewAttemptPaths{
				PromptFile:  revisionPromptPath,
				OutFile:     revisionOutPath,
				ExitFile:    revisionExitPath,
				HistoryFile: paths.HistoryFile,
			}
			revisionPrompt := renderReviewContractRevisionPrompt(prompt, output, contractErr)
			if writeErr := writeTextAtomic(revisionPaths.PromptFile, revisionPrompt); writeErr != nil {
				return nil, "", paths, writeErr
			}
			_ = AppendAgentMessage(agent, AgentMessage{Role: "user", Content: revisionPrompt})
			revisionOutput, revisionErr := runReviewCodexPrompt(ctx, repoRoot, revisionPrompt, 30*time.Minute)
			revisionExitCode := 0
			if revisionErr != nil {
				revisionExitCode = 1
				revisionOutput = revisionErr.Error()
			}
			_ = os.WriteFile(revisionPaths.OutFile, []byte(revisionOutput), 0644)
			_ = os.WriteFile(revisionPaths.ExitFile, []byte(fmt.Sprintf("%d\n", revisionExitCode)), 0644)
			_ = AppendAgentMessage(agent, AgentMessage{Role: "assistant", Content: revisionOutput})
			revisedReport := ParseReviewReport(revisionOutput)
			revisionFailure := FailureClassNone
			rejectionReason := ""
			if revisionErr != nil {
				revisedReport.Passed = false
				rejectionReason = revisionErr.Error()
			} else if secondContractErr := ValidateReviewReport(revisedReport, []*SubtaskResult{result}, result.Subtask.ID); secondContractErr != nil {
				revisedReport.Passed = false
				revisionFailure = FailureClassContractError
				rejectionReason = secondContractErr.Error()
				revisedReport.Issues = append(revisedReport.Issues, ReviewIssue{
					SubtaskID:    result.Subtask.ID,
					SessionID:    paths.SessionID,
					Severity:     "blocking",
					FailureClass: FailureClassContractError,
					Description:  secondContractErr.Error(),
				})
			}
			_ = writeReviewAttempt(repoRoot, runID, "review:subtask:"+result.Subtask.ID, paths.SessionID, result.Subtask.ID, 1, 0, "contract_error", revisionFailure, rejectionReason, revisionPaths)
			return revisedReport, revisionOutput, paths, nil
		}
	}
	_ = writeReviewAttempt(repoRoot, runID, "review:subtask:"+result.Subtask.ID, paths.SessionID, result.Subtask.ID, 0, -1, "initial", FailureClassNone, "", paths.ReviewAttemptPaths)
	if revisedReport, revisedOutput, revised, reviseErr := runReviewHumanRejectRevision(ctx, repoRoot, runID, "review:subtask:"+result.Subtask.ID, paths.SessionID, result.Subtask.ID, prompt, output, paths.ReviewAttemptPaths, func(report *ReviewReport) error {
		return ValidateReviewReport(report, []*SubtaskResult{result}, result.Subtask.ID)
	}); reviseErr != nil {
		return nil, "", paths, reviseErr
	} else if revised {
		return revisedReport, revisedOutput, paths, nil
	}
	return report, output, paths, nil
}

func RunAggregateReview(ctx context.Context, repoRoot, runID string, plan *Plan, subtaskReports map[string]*ReviewReport) (*ReviewReport, string, ReviewAggregatePaths, error) {
	paths := NewReviewAggregatePaths(repoRoot, runID)
	if err := os.MkdirAll(paths.Dir, 0755); err != nil {
		return nil, "", paths, err
	}
	session := AgentSession{
		ID:          paths.SessionID,
		AgentType:   "review",
		Status:      AgentSessionRunning,
		HistoryFile: paths.HistoryFile,
	}
	RegisterAgentSession(session)
	prompt := renderAggregateReviewPrompt(plan, subtaskReports)
	if err := writeTextAtomic(paths.PromptFile, prompt); err != nil {
		return nil, "", paths, err
	}
	agent := &SubtaskAgent{Session: session}
	_ = AppendAgentMessage(agent, AgentMessage{Role: "system", Content: prompt})
	_ = appendReviewApprovalRequest(agent, "review:aggregate")
	output, err := runReviewCodexPrompt(ctx, repoRoot, prompt, 30*time.Minute)
	exitCode := 0
	if err != nil {
		exitCode = 1
		output = err.Error()
	}
	_ = os.WriteFile(paths.OutFile, []byte(output), 0644)
	_ = os.WriteFile(paths.ExitFile, []byte(fmt.Sprintf("%d\n", exitCode)), 0644)
	_ = AppendAgentMessage(agent, AgentMessage{Role: "assistant", Content: output})
	report := ParseReviewReport(output)
	if err != nil {
		report.Passed = false
		report.Issues = append(report.Issues, ReviewIssue{Severity: "blocking", Description: err.Error()})
	}
	_ = writeReviewAttempt(repoRoot, runID, "review:aggregate", paths.SessionID, "", 0, -1, "initial", FailureClassNone, "", paths.ReviewAttemptPaths)
	if revisedReport, revisedOutput, revised, reviseErr := runReviewHumanRejectRevision(ctx, repoRoot, runID, "review:aggregate", paths.SessionID, "", prompt, output, paths.ReviewAttemptPaths, nil); reviseErr != nil {
		return nil, "", paths, reviseErr
	} else if revised {
		return revisedReport, revisedOutput, paths, nil
	}
	return report, output, paths, nil
}

func appendReviewApprovalRequest(agent *SubtaskAgent, nodeName string) error {
	request := ApprovalRequest{
		RunID:     runIDFromSessionID(agent.Session.ID),
		SessionID: agent.Session.ID,
		NodeName:  nodeName,
		Status:    "WAITING",
		Timestamp: time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return AppendAgentMessage(agent, AgentMessage{Role: "approval_request", Content: string(data)})
}

func runReviewHumanRejectRevision(ctx context.Context, repoRoot, runID, scope, sessionID, subtaskID, originalPrompt, output string, paths ReviewAttemptPaths, validate func(*ReviewReport) error) (*ReviewReport, string, bool, error) {
	decision, ok, err := LatestReviewDecision(sessionID, scope)
	if err != nil || !ok || decision.Approved {
		return nil, "", false, err
	}
	revisionPromptPath, revisionOutPath, revisionExitPath := AttemptPathsForRevision(paths.PromptFile, paths.OutFile, paths.ExitFile, 1)
	revisionPaths := ReviewAttemptPaths{
		PromptFile:  revisionPromptPath,
		OutFile:     revisionOutPath,
		ExitFile:    revisionExitPath,
		HistoryFile: paths.HistoryFile,
	}
	revisionPrompt := renderReviewHumanRejectRevisionPrompt(originalPrompt, output, decision)
	if err := writeTextAtomic(revisionPaths.PromptFile, revisionPrompt); err != nil {
		return nil, "", false, err
	}
	agent := &SubtaskAgent{SubtaskID: subtaskID, Session: AgentSession{ID: sessionID, HistoryFile: paths.HistoryFile}}
	_ = AppendAgentMessage(agent, AgentMessage{Role: "user", Content: revisionPrompt})
	revisionOutput, revisionErr := runReviewCodexPrompt(ctx, repoRoot, revisionPrompt, 30*time.Minute)
	revisionExitCode := 0
	if revisionErr != nil {
		revisionExitCode = 1
		revisionOutput = revisionErr.Error()
	}
	_ = os.WriteFile(revisionPaths.OutFile, []byte(revisionOutput), 0644)
	_ = os.WriteFile(revisionPaths.ExitFile, []byte(fmt.Sprintf("%d\n", revisionExitCode)), 0644)
	_ = AppendAgentMessage(agent, AgentMessage{Role: "assistant", Content: revisionOutput})
	report := ParseReviewReport(revisionOutput)
	if revisionErr != nil {
		report.Passed = false
		report.Issues = append(report.Issues, ReviewIssue{SubtaskID: subtaskID, Severity: "blocking", FailureClass: FailureClassHumanReject, Description: revisionErr.Error()})
	} else if validate != nil {
		if contractErr := validate(report); contractErr != nil {
			report.Passed = false
			report.Issues = append(report.Issues, ReviewIssue{SubtaskID: subtaskID, Severity: "blocking", FailureClass: FailureClassContractError, Description: contractErr.Error()})
		}
	}
	_ = writeReviewAttempt(repoRoot, runID, scope, sessionID, subtaskID, 1, 0, "human_reject", FailureClassHumanReject, decision.Reason, revisionPaths)
	return report, revisionOutput, true, nil
}

func MergeReviewReports(subtaskReports map[string]*ReviewReport, aggregate *ReviewReport) *ReviewReport {
	merged := &ReviewReport{
		Passed:         true,
		SubtaskReports: map[string]*ReviewReport{},
	}
	var summaries []string
	var ids []string
	for id := range subtaskReports {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		report := subtaskReports[id]
		merged.SubtaskReports[id] = report
		if report == nil {
			merged.Passed = false
			continue
		}
		if strings.TrimSpace(report.Summary) != "" {
			summaries = append(summaries, id+": "+report.Summary)
		}
		if !report.Passed {
			merged.Passed = false
		}
		merged.Issues = append(merged.Issues, report.Issues...)
	}
	if aggregate != nil {
		if strings.TrimSpace(aggregate.Summary) != "" {
			summaries = append(summaries, "aggregate: "+aggregate.Summary)
		}
		if !aggregate.Passed {
			merged.Passed = false
		}
		merged.Issues = append(merged.Issues, aggregate.Issues...)
	}
	merged.Summary = strings.Join(summaries, "\n")
	for _, issue := range merged.Issues {
		if normalizeSeverity(issue.Severity) == "blocking" {
			merged.Passed = false
			break
		}
	}
	return merged
}

func writeReviewAttempt(repoRoot, runID, scope, sessionID, subtaskID string, attempt, parentAttempt int, trigger string, failureClass FailureClass, rejectionReason string, paths ReviewAttemptPaths) error {
	now := time.Now().UTC()
	event := AttemptEvent{
		RunID:      runID,
		NodeID:     "review",
		NodeKind:   NodeKindReview,
		Scope:      scope,
		SubtaskID:  subtaskID,
		Role:       "review",
		SessionID:  sessionID,
		Attempt:    attempt,
		Trigger:    trigger,
		StartedAt:  now,
		FinishedAt: now,
		PromptPath: paths.PromptFile,
		OutputPath: paths.OutFile,
		ExitPath:   paths.ExitFile,
		ArtifactRefs: []ArtifactRef{
			{Type: ArtifactTypePrompt, Path: paths.PromptFile},
			{Type: ArtifactTypeOutput, Path: paths.OutFile},
			{Type: ArtifactTypeExit, Path: paths.ExitFile},
			{Type: ArtifactTypeSession, Path: paths.HistoryFile},
		},
	}
	if parentAttempt >= 0 {
		event.ParentAttempt = &parentAttempt
	}
	if failureClass != "" && failureClass != FailureClassNone {
		event.FailureClass = failureClass
		event.FailureReason = string(failureClass)
	}
	if rejectionReason != "" {
		event.RejectionReason = rejectionReason
	}
	return RecordAttemptEvent(repoRoot, event)
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
