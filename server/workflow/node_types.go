package workflow

import (
	"context"
	"sync"
	"time"
)

type NodeKind string

const (
	NodeKindClarification NodeKind = "clarification"
	NodeKindPlanning      NodeKind = "planning"
	NodeKindDevelopment   NodeKind = "development"
	NodeKindEvaluation    NodeKind = "evaluation"
	NodeKindReview        NodeKind = "review"
)

type WorkflowNode interface {
	Kind() NodeKind
	Execute(ctx context.Context, req NodeRequest) (*NodeResult, error)
}

type FailureClass string

const (
	FailureClassNone               FailureClass = "none"
	FailureClassHumanReject        FailureClass = "human_reject"
	FailureClassTransient          FailureClass = "transient"
	FailureClassMissingEvidence    FailureClass = "missing_evidence"
	FailureClassContractError      FailureClass = "contract_error"
	FailureClassInconclusive       FailureClass = "inconclusive"
	FailureClassProductDefect      FailureClass = "product_defect"
	FailureClassEnvironmentBlock   FailureClass = "environment_block"
	FailureClassValidatorIssue     FailureClass = "validator_issue"
	FailureClassInvalidInput       FailureClass = "invalid_input"
	FailureClassUserBlocked        FailureClass = "user_blocked"
	FailureClassUnsafeOrOutOfScope FailureClass = "unsafe_or_out_of_scope"
)

type RollbackAction string

const (
	RollbackActionFixClarification  RollbackAction = "fix_clarification"
	RollbackActionRegeneratePlan    RollbackAction = "regenerate_plan"
	RollbackActionRegenerateSubtask RollbackAction = "regenerate_subtask"
	RollbackActionFixEnvironment    RollbackAction = "fix_environment"
	RollbackActionRetryGenerator    RollbackAction = "retry_generator"
	RollbackActionAskUser           RollbackAction = "ask_user"
	RollbackActionBlocked           RollbackAction = "blocked"
)

type SessionReusePolicy string

const (
	SessionReuseSameSession   SessionReusePolicy = "same_session"
	SessionReuseNewSession    SessionReusePolicy = "new_session"
	SessionReuseOnHumanReject SessionReusePolicy = "reuse_on_human_reject"
)

type RollbackUnit struct {
	Scope       string
	MaxAttempts int
	ReusePolicy SessionReusePolicy
	ParentScope string
}

type RollbackSignal struct {
	FailureClass  FailureClass
	Reason        string
	Suggestion    string
	SourceNode    string
	SourceAttempt int
}

type RollbackDecision struct {
	Action       RollbackAction
	TargetScope  string
	NewAttempt   int
	ReuseSession bool
	Rationale    string
}

type RollbackContext struct {
	RunID    string
	NodeID   string
	NodeKind NodeKind
	Unit     RollbackUnit
	Budget   RollbackBudget
	Lineage  AttemptLineageStore
	Decision *RollbackDecision
}

type RollbackSpec struct {
	DefaultUnit     RollbackUnit
	OnContract      RollbackAction
	OnHumanReject   RollbackAction
	OnProductDefect RollbackAction
}

type RollbackBudget struct {
	MaxAttempts     int
	UsedAttempts    int
	ExhaustedAction RollbackAction
}

type RollbackCapableNode interface {
	WorkflowNode
	PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error)
	RollbackArtifacts(rctx RollbackContext) []ArtifactRef
}

// AttemptLineageStore is implemented by the attempt lineage package in P1-T03.
type AttemptLineageStore interface {
	Append(event AttemptEvent) error
	List(filter AttemptFilter) ([]AttemptEvent, error)
}

type NodeRequest struct {
	RunID    string
	RepoRoot string
	Spec     NodeSpec
	Config   map[string]any
	Inputs   NodeInputs
	Services NodeServices
}

type NodeInputs map[string]NodeOutput

type NodeOutput struct {
	NodeID  string
	Values  map[string]any
	Outputs map[string]string
}

type NodeResult struct {
	NodeID       string
	Status       NodeStatus
	Values       map[string]any
	OutputPaths  []string
	Error        string
	BlockedItems []BlockedItem
}

type NodeSpec struct {
	ID        string
	Kind      NodeKind
	DependsOn []string
	Config    map[string]any
}

type NodeServices struct {
	ApprovalGate     ApprovalGate
	StatusWriter     StatusWriter
	NotificationSink NotificationSink
	PtyReader        PtyReader
	SessionManager   SessionManager
}

const (
	NodeStatusPending   NodeStatus = "pending"
	NodeStatusRunning   NodeStatus = "running"
	NodeStatusCompleted NodeStatus = "completed"
	NodeStatusFailed    NodeStatus = "failed"
	NodeStatusBlocked   NodeStatus = "blocked"
	NodeStatusSkipped   NodeStatus = "skipped"
)

type ApprovalGate interface {
	Wait(ctx context.Context, runID, nodeID string) error
}

type StatusWriter interface {
	WriteState(repoRoot, runID string, nodes map[string]NodeStatus, details map[string]any) error
	WritePhase(runID string, status *PhaseStatus) error
}

type NotificationSink interface {
	Emit(sessionID string, notification NodeNotification) error
}

type PtyReader interface {
	Subscribe(sid string) (<-chan []byte, error)
	Unsubscribe(sid string) error
}

type SessionID string

type SessionConfig struct {
	WorkDir   string
	AgentType string
	Env       map[string]string
}

type SessionManager interface {
	StartSession(ctx context.Context, config SessionConfig) (SessionID, error)
	SendInput(sid SessionID, data string) error
	WaitForPrompt(sid SessionID, timeout time.Duration) error
	Resize(sid SessionID, cols, rows uint16) error
	SendAndWait(sid SessionID, input string, timeout time.Duration) (string, error)
	Close(sid SessionID) error
}

type NodeFactory func() WorkflowNode

type NodeRegistry struct {
	mu        sync.RWMutex
	factories map[NodeKind]NodeFactory
}
