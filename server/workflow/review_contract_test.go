package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRenderSubtaskReviewPromptFocusesOneSubtask(t *testing.T) {
	plan := &Plan{
		TaskID: "run-1",
		Subtasks: []Subtask{
			{
				ID:                 "api",
				RepoPath:           "server/api",
				Description:        "Implement API validation.",
				AcceptanceCriteria: []string{"reject invalid requests"},
				PlannedFiles:       []string{"server/api/handler.go"},
			},
			{
				ID:                 "web",
				RepoPath:           "web",
				Description:        "Do not include this subtask.",
				AcceptanceCriteria: []string{"other criterion"},
				PlannedFiles:       []string{"web/app.ts"},
			},
		},
	}
	result := &SubtaskResult{
		Subtask:   plan.Subtasks[0],
		SessionID: "session-api",
		OutFile:   "/tmp/api.out.md",
		ExitCode:  0,
		Status:    "completed",
		Output:    "large development output must not be embedded",
	}

	prompt := renderSubtaskReviewPrompt(plan, result, "/tmp/review.diff")

	for _, want := range []string{
		"subtask_id: api",
		"session_id: session-api",
		"repo_path: server/api",
		"server/api/handler.go",
		"reject invalid requests",
		"/tmp/review.diff",
		"required_fixes",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"Do not include this subtask", "web/app.ts", "large development output"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt includes unrelated or embedded output %q:\n%s", forbidden, prompt)
		}
	}
}

func TestValidateReviewReportRequiresBlockingIssueContract(t *testing.T) {
	results := []*SubtaskResult{{Subtask: Subtask{ID: "api"}, SessionID: "session-api"}}
	report := &ReviewReport{Passed: false, Issues: []ReviewIssue{{
		Severity:    "blocking",
		Description: "missing ownership and fixes",
	}}}

	err := ValidateReviewReport(report, results, "api")
	if err == nil {
		t.Fatal("ValidateReviewReport() error = nil, want contract error")
	}
	var classified interface {
		FailureClass() FailureClass
	}
	if !errors.As(err, &classified) {
		t.Fatalf("ValidateReviewReport() error %T missing FailureClass()", err)
	}
	if classified.FailureClass() != FailureClassContractError {
		t.Fatalf("FailureClass() = %q, want %q", classified.FailureClass(), FailureClassContractError)
	}
}

func TestValidateReviewReportAcceptsOwnedBlockingIssueWithRequiredFixes(t *testing.T) {
	results := []*SubtaskResult{{Subtask: Subtask{ID: "api"}, SessionID: "session-api"}}
	report := &ReviewReport{Passed: false, Issues: []ReviewIssue{{
		Severity:      "blocking",
		SubtaskID:     "api",
		SessionID:     "session-api",
		Description:   "handler misses validation",
		RequiredFixes: []string{"add validation"},
	}}}

	if err := ValidateReviewReport(report, results, "api"); err != nil {
		t.Fatalf("ValidateReviewReport() error = %v", err)
	}
}

func TestRunSubtaskReviewRevisesInvalidContractOnce(t *testing.T) {
	repoRoot := t.TempDir()
	plan := &Plan{TaskID: "run-contract", Subtasks: []Subtask{{ID: "api", RepoPath: "server/api"}}}
	result := &SubtaskResult{Subtask: plan.Subtasks[0], SessionID: "session-api", Status: "completed"}
	calls := 0
	oldRunner := runReviewCodexPrompt
	runReviewCodexPrompt = func(ctx context.Context, repoRoot, prompt string, timeout time.Duration) (string, error) {
		calls++
		if calls == 1 {
			return "## Summary\nbad\n\n## Issues\n- severity: blocking\n  description: no fixes\n", nil
		}
		if !strings.Contains(prompt, "Fix only the review report contract") {
			t.Fatalf("revision prompt missing contract-only instruction:\n%s", prompt)
		}
		return "## Summary\nfixed\n\n## Issues\n- severity: blocking\n  subtask_id: api\n  session_id: session-api\n  description: still blocked\n  required_fixes: add validation\n", nil
	}
	defer func() { runReviewCodexPrompt = oldRunner }()

	report, _, paths, err := RunSubtaskReview(context.Background(), repoRoot, plan.TaskID, plan, result, "/tmp/diff")
	if err != nil {
		t.Fatalf("RunSubtaskReview() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("review runner calls = %d, want 2", calls)
	}
	if len(report.Issues) != 1 || report.Issues[0].RequiredFixes[0] != "add validation" {
		t.Fatalf("report = %#v, want revised valid issue", report)
	}
	events, err := ReadAttemptEvents(repoRoot, plan.TaskID, "review")
	if err != nil {
		t.Fatalf("ReadAttemptEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("attempt events = %d, want initial contract error plus revision", len(events))
	}
	if events[0].FailureClass != FailureClassContractError {
		t.Fatalf("first attempt FailureClass = %q, want %q", events[0].FailureClass, FailureClassContractError)
	}
	if events[1].ParentAttempt == nil || *events[1].ParentAttempt != 0 {
		t.Fatalf("revision ParentAttempt = %#v, want 0", events[1].ParentAttempt)
	}
	if !strings.Contains(events[1].PromptPath, "-revision-1.prompt.md") || events[1].SessionID != paths.SessionID {
		t.Fatalf("revision event = %#v, want revision artifact and same session", events[1])
	}
}

func TestMergeReviewReportsFailsOnAnyBlockingIssue(t *testing.T) {
	subtaskReports := map[string]*ReviewReport{
		"api": {Passed: true, Summary: "api ok"},
		"web": {Passed: false, Summary: "web blocked", Issues: []ReviewIssue{{
			SubtaskID:     "web",
			Severity:      "blocking",
			Description:   "web issue",
			RequiredFixes: []string{"fix web"},
		}}},
	}
	aggregate := &ReviewReport{Passed: false, Summary: "aggregate blocked", Issues: []ReviewIssue{{
		Severity:      "blocking",
		Description:   "shared docs mismatch",
		RequiredFixes: []string{"update docs"},
	}}}

	merged := MergeReviewReports(subtaskReports, aggregate)
	if merged.Passed {
		t.Fatal("Merged Passed = true, want false")
	}
	if len(merged.Issues) != 2 {
		t.Fatalf("merged issues = %d, want 2", len(merged.Issues))
	}
	if merged.Issues[1].SubtaskID != "" {
		t.Fatalf("aggregate issue SubtaskID = %q, want run-level issue", merged.Issues[1].SubtaskID)
	}
	if len(merged.SubtaskReports) != 2 {
		t.Fatalf("SubtaskReports len = %d, want 2", len(merged.SubtaskReports))
	}
}
