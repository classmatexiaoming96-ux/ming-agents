package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAttemptEventJSONRoundTrip(t *testing.T) {
	started := time.Now().UTC().Truncate(time.Second)
	event := AttemptEvent{
		RunID:           "run-1",
		NodeID:          "review",
		NodeKind:        NodeKindReview,
		Scope:           "review:subtask-api",
		SubtaskID:       "api",
		Role:            "assistant",
		SessionID:       "session-1",
		Attempt:         0,
		ParentAttempt:   0,
		Trigger:         "contract_error",
		FailureClass:    FailureClassContractError,
		FailureReason:   "missing required_fixes",
		RejectionReason: "needs detail",
		RetryAdvice:     "revise review report",
		PromptPath:      "/tmp/prompt.md",
		OutputPath:      "/tmp/out.md",
		ExitPath:        "/tmp/exit",
		ArtifactRefs: []ArtifactRef{
			{Type: "prompt", Path: "/tmp/prompt.md", SubtaskID: "api", Description: "review prompt"},
		},
		PromptDelta: &AttemptPromptDelta{
			AddedFeedback: "include required_fixes",
			RemovedBlocks: []string{"old-block"},
		},
		Decision: &RollbackDecision{
			Action:      RollbackActionAskUser,
			TargetScope: "review:subtask-api",
			NewAttempt:  1,
			Rationale:   "contract error",
		},
		NextAction: NextActionRetryReport,
		Outcome: &AttemptOutcome{
			Status:       "failed",
			FailureClass: FailureClassContractError,
			Reason:       "contract error",
			ArtifactRefs: []ArtifactRef{{Type: "output", Path: "/tmp/out.md", SubtaskID: "api"}},
		},
		StartedAt:  started,
		FinishedAt: started.Add(time.Second),
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got AttemptEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.SubtaskID != event.SubtaskID {
		t.Fatalf("SubtaskID = %q, want %q", got.SubtaskID, event.SubtaskID)
	}
	if got.FailureClass != event.FailureClass {
		t.Fatalf("FailureClass = %q, want %q", got.FailureClass, event.FailureClass)
	}
	if len(got.ArtifactRefs) != 1 || got.ArtifactRefs[0].SubtaskID != "api" {
		t.Fatalf("ArtifactRefs = %+v, want subtask artifact", got.ArtifactRefs)
	}
	if got.PromptDelta == nil || got.PromptDelta.AddedFeedback == "" {
		t.Fatalf("PromptDelta = %+v, want added feedback", got.PromptDelta)
	}
	if got.Outcome == nil || got.Outcome.FailureClass != FailureClassContractError {
		t.Fatalf("Outcome = %+v, want contract error", got.Outcome)
	}
}

func TestArtifactRefDistinctFromEvidenceRef(t *testing.T) {
	evidence := EvidenceRef{Type: "test_log", Path: "/tmp/test.log"}
	artifact := ArtifactRef{Type: "prompt", Path: "/tmp/prompt.md", SubtaskID: "api"}
	if evidence.Type == artifact.Type {
		t.Fatalf("EvidenceRef type %q should differ from ArtifactRef type %q", evidence.Type, artifact.Type)
	}
	if artifact.SubtaskID != "api" {
		t.Fatalf("ArtifactRef SubtaskID = %q, want api", artifact.SubtaskID)
	}
}

func TestAppendAttemptEvent(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "run-1"
	nodeID := "review"

	event := AttemptEvent{
		RunID:     runID,
		NodeID:    nodeID,
		NodeKind:  NodeKindReview,
		Scope:     "review:subtask-api",
		SubtaskID: "api",
		Attempt:   0,
		StartedAt: time.Now().UTC(),
	}
	if err := AppendAttemptEvent(tmpDir, event); err != nil {
		t.Fatal(err)
	}

	nodePath := filepath.Join(tmpDir, ".workflow", "runs", runID, nodeID, "attempts.jsonl")
	indexPath := filepath.Join(tmpDir, ".workflow", "runs", runID, "attempts.index.jsonl")
	scopePath := filepath.Join(tmpDir, ".workflow", "runs", runID, nodeID, "attempts", "review_subtask-api.jsonl")

	for _, path := range []string{nodePath, indexPath, scopePath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}
}

func TestAppendAttemptEventMultipleEvents(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "run-1"
	nodeID := "development"

	for i := 0; i < 3; i++ {
		event := AttemptEvent{
			RunID:     runID,
			NodeID:    nodeID,
			NodeKind:  NodeKindDevelopment,
			Attempt:   i,
			StartedAt: time.Now().UTC(),
		}
		if err := AppendAttemptEvent(tmpDir, event); err != nil {
			t.Fatal(err)
		}
	}

	events, err := ReadAttemptEvents(tmpDir, runID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	for i, event := range events {
		if event.Attempt != i {
			t.Errorf("events[%d].Attempt = %d, want %d", i, event.Attempt, i)
		}
	}

	nodePath := filepath.Join(tmpDir, ".workflow", "runs", runID, nodeID, "attempts.jsonl")
	data, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3", len(lines))
	}
	for i, line := range lines {
		var event AttemptEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestSafeScopeCleansPathSeparators(t *testing.T) {
	cases := map[string]string{
		"review:subtask-api/foo":  "review_subtask-api_foo",
		"approval:plan\\validate": "approval_plan_validate",
		"subtask api has space":   "subtask_api_has_space",
		"safe-scope-name":         "safe-scope-name",
		"":                        "",
	}
	for in, want := range cases {
		if got := safeScope(in); got != want {
			t.Errorf("safeScope(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmptyScopeDoesNotCreateAttemptsDir(t *testing.T) {
	tmpDir := t.TempDir()
	event := AttemptEvent{
		RunID:     "run-1",
		NodeID:    "dev",
		NodeKind:  NodeKindDevelopment,
		Scope:     "",
		Attempt:   0,
		StartedAt: time.Now().UTC(),
	}
	if err := AppendAttemptEvent(tmpDir, event); err != nil {
		t.Fatal(err)
	}

	attemptsDir := filepath.Join(tmpDir, ".workflow", "runs", "run-1", "dev", "attempts")
	if _, err := os.Stat(attemptsDir); !os.IsNotExist(err) {
		t.Errorf("attempts dir should not exist for empty scope, stat err=%v", err)
	}
}

func TestFileLineageStoreListAppliesFilter(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileLineageStore(tmpDir)
	events := []AttemptEvent{
		{RunID: "run-1", NodeID: "review", NodeKind: NodeKindReview, Scope: "review:subtask-api", SubtaskID: "api", Attempt: 0, StartedAt: time.Now().UTC()},
		{RunID: "run-1", NodeID: "review", NodeKind: NodeKindReview, Scope: "review:subtask-web", SubtaskID: "web", Attempt: 1, StartedAt: time.Now().UTC()},
		{RunID: "run-1", NodeID: "review", NodeKind: NodeKindReview, Scope: "review:subtask-api", SubtaskID: "api", Attempt: 2, StartedAt: time.Now().UTC()},
	}
	for _, event := range events {
		if err := store.Append(event); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.List(AttemptFilter{RunID: "run-1", NodeID: "review", SubtaskID: "api", Scope: "review:subtask-api", FromAttempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Attempt != 2 || got[0].SubtaskID != "api" {
		t.Fatalf("got[0] = %+v, want api attempt 2", got[0])
	}
}

func TestAppendAttemptEventConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	const total = 100

	var wg sync.WaitGroup
	errs := make(chan error, total)
	for i := 0; i < total; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- AppendAttemptEvent(tmpDir, AttemptEvent{
				RunID:     "run-concurrent",
				NodeID:    "review",
				NodeKind:  NodeKindReview,
				Scope:     "review:subtask-api",
				SubtaskID: "api",
				Attempt:   i,
				StartedAt: time.Now().UTC(),
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AppendAttemptEvent() error = %v", err)
		}
	}

	events, err := ReadAttemptEvents(tmpDir, "run-concurrent", "review")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != total {
		t.Fatalf("len(events) = %d, want %d", len(events), total)
	}
	seen := map[int]bool{}
	for _, event := range events {
		seen[event.Attempt] = true
	}
	for i := 0; i < total; i++ {
		if !seen[i] {
			t.Fatalf("missing attempt %d in concurrent append results", i)
		}
	}
}

func TestAppendAttemptEventRejectsUnsafePathIDs(t *testing.T) {
	tmpDir := t.TempDir()
	cases := []struct {
		name   string
		runID  string
		nodeID string
	}{
		{name: "empty run", runID: "", nodeID: "review"},
		{name: "slash", runID: "run/1", nodeID: "review"},
		{name: "backslash", runID: "run\\1", nodeID: "review"},
		{name: "dotdot", runID: "run..1", nodeID: "review"},
		{name: "space", runID: "run 1", nodeID: "review"},
		{name: "chinese", runID: "运行", nodeID: "review"},
		{name: "emoji", runID: "run🙂", nodeID: "review"},
		{name: "unsafe node", runID: "run-1", nodeID: "../review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := AppendAttemptEvent(tmpDir, AttemptEvent{
				RunID:     tc.runID,
				NodeID:    tc.nodeID,
				NodeKind:  NodeKindReview,
				Attempt:   0,
				StartedAt: time.Now().UTC(),
			})
			if err == nil {
				t.Fatal("AppendAttemptEvent() error = nil, want invalid path id error")
			}
		})
	}
}
