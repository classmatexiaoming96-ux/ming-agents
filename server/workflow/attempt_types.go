package workflow

import "time"

type AttemptEvent struct {
	RunID           string              `json:"run_id"`
	NodeID          string              `json:"node_id"`
	NodeKind        NodeKind            `json:"node_kind"`
	Scope           string              `json:"scope"`
	SubtaskID       string              `json:"subtask_id,omitempty"`
	Role            string              `json:"role"`
	SessionID       string              `json:"session_id,omitempty"`
	Attempt         int                 `json:"attempt"`
	ParentAttempt   int                 `json:"parent_attempt,omitempty"`
	Trigger         string              `json:"trigger,omitempty"`
	FailureClass    FailureClass        `json:"failure_class,omitempty"`
	FailureReason   string              `json:"failure_reason,omitempty"`
	RejectionReason string              `json:"rejection_reason,omitempty"`
	RetryAdvice     string              `json:"retry_advice,omitempty"`
	PromptPath      string              `json:"prompt_path,omitempty"`
	OutputPath      string              `json:"output_path,omitempty"`
	ExitPath        string              `json:"exit_path,omitempty"`
	ArtifactRefs    []ArtifactRef       `json:"artifact_refs,omitempty"`
	PromptDelta     *AttemptPromptDelta `json:"prompt_delta,omitempty"`
	Decision        *RollbackDecision   `json:"decision,omitempty"`
	NextAction      string              `json:"next_action,omitempty"`
	Outcome         *AttemptOutcome     `json:"outcome,omitempty"`
	StartedAt       time.Time           `json:"started_at"`
	FinishedAt      time.Time           `json:"finished_at,omitempty"`
}

type AttemptPromptDelta struct {
	AddedIssues   []ReviewIssue `json:"added_issues,omitempty"`
	AddedFeedback string        `json:"added_feedback,omitempty"`
	RemovedBlocks []string      `json:"removed_blocks,omitempty"`
}

type AttemptOutcome struct {
	Status       string        `json:"status"`
	Passed       bool          `json:"passed,omitempty"`
	FailureClass FailureClass  `json:"failure_class,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	ArtifactRefs []ArtifactRef `json:"artifact_refs,omitempty"`
}

type ArtifactRef struct {
	Type        string `json:"type"`
	Path        string `json:"path"`
	SubtaskID   string `json:"subtask_id,omitempty"`
	Description string `json:"description,omitempty"`
}

type AttemptFilter struct {
	RunID       string
	NodeID      string
	SubtaskID   string
	Scope       string
	FromAttempt int
}
