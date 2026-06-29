package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ming-agents/server/adapter"
)

func TestRenderReviewPromptUsesArtifactPathsInsteadOfDevelopmentOutput(t *testing.T) {
	plan := &Plan{TaskID: "run-review", Subtasks: []Subtask{{
		ID:                 "api",
		Description:        "implement api",
		AcceptanceCriteria: []string{"tests pass", "output documented"},
	}}}
	results := []*SubtaskResult{{
		Subtask:   plan.Subtasks[0],
		SessionID: "dev-session",
		Status:    "completed",
		ExitCode:  0,
		OutFile:   ".workflow/runs/run-review/node3/dev-1.out.md",
		Output:    "SECRET DEVELOPMENT SESSION OUTPUT",
	}}

	prompt := renderReviewPrompt(plan, results, ".workflow/runs/run-review/node3/review.diff")
	if strings.Contains(prompt, "SECRET DEVELOPMENT SESSION OUTPUT") {
		t.Fatalf("review prompt included development output: %s", prompt)
	}
	for _, want := range []string{
		".workflow/runs/run-review/node3/dev-1.out.md",
		"tests pass",
		"output documented",
		".workflow/runs/run-review/node3/review.diff",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review prompt missing %q: %s", want, prompt)
		}
	}
}

func TestFindCodeDirsReturnsRunCodeArtifactDirs(t *testing.T) {
	repoRoot := t.TempDir()
	runID := "run-code-dirs"
	for _, dir := range []string{"code", "src", "changes", "node3"} {
		if err := os.MkdirAll(filepath.Join(repoRoot, ".workflow", "runs", runID, dir), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	dirs := findCodeDirs(repoRoot, runID)
	if len(dirs) != 3 {
		t.Fatalf("findCodeDirs() = %#v, want 3 code dirs", dirs)
	}
	for _, want := range []string{"changes", "code", "src"} {
		found := false
		for _, got := range dirs {
			if filepath.Base(got) == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("findCodeDirs() = %#v, missing %s", dirs, want)
		}
	}
}

func TestEvaluationResultFromVerificationClassifiesRawVerdict(t *testing.T) {
	result := evaluationResultFromVerification(&adapter.VerificationResult{
		RunID:       "run-verdict",
		EvaluatedAt: time.Now(),
		Verdict:     "FAIL",
		Reason:      "missing output",
	})
	if result.Passed {
		t.Fatal("Passed = true, want false")
	}
	if result.FailureClass != "product_defect" {
		t.Fatalf("FailureClass = %q, want product_defect", result.FailureClass)
	}
	if !strings.Contains(result.RetryAdvice, "missing output") {
		t.Fatalf("RetryAdvice = %q, want reason included", result.RetryAdvice)
	}

	result = evaluationResultFromVerification(&adapter.VerificationResult{
		RunID:       "run-verdict",
		EvaluatedAt: time.Now(),
		Verdict:     "ERROR",
		Reason:      "session closed",
	})
	if result.FailureClass != "validator_issue" {
		t.Fatalf("FailureClass = %q, want validator_issue", result.FailureClass)
	}
}
