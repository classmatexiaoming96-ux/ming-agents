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
