package workflow

import (
	"encoding/json"
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
		NextAction: "retry_report",
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
