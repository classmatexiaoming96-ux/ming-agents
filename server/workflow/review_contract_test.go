package workflow

import (
	"strings"
	"testing"
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
