package workflow

import "time"

type Plan struct {
	TaskID   string    `json:"task_id"`
	Subtasks []Subtask `json:"subtasks"`
}

type Subtask struct {
	ID                 string   `json:"id"`
	AgentType          string   `json:"agent_type"`
	RepoPath           string   `json:"repo_path"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	PlannedFiles       []string `json:"planned_files,omitempty"`
}

type SubtaskResult struct {
	Subtask   Subtask
	SessionID string
	Agent     *SubtaskAgent
	OutFile   string
	ExitFile  string
	ExitCode  int
	Status    string
	Output    string
	Err       error
}

type ReviewReport struct {
	Passed         bool                     `json:"passed"`
	Summary        string                   `json:"summary"`
	Issues         []ReviewIssue            `json:"issues"`
	SubtaskReports map[string]*ReviewReport `json:"subtask_reports,omitempty"`
}

type ReviewIssue struct {
	SubtaskID     string   `json:"subtask_id,omitempty"`
	SessionID     string   `json:"session_id,omitempty"`
	Severity      string   `json:"severity"`
	FailureClass  string   `json:"failure_class,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
	Description   string   `json:"description"`
	RequiredFixes []string `json:"required_fixes,omitempty"`
}

type NodeStatus string

const (
	NodePending       NodeStatus = "pending"
	NodeRunning       NodeStatus = "running"
	NodeWaitingReview NodeStatus = "waiting_review"
	NodeCompleted     NodeStatus = "completed"
	NodeFailed        NodeStatus = "failed"
)

type RunState struct {
	RunID   string                `json:"run_id"`
	Nodes   map[string]NodeStatus `json:"nodes"`
	Details map[string]any        `json:"details,omitempty"`
}

type AgentSessionStatus string

const (
	AgentSessionPending     AgentSessionStatus = "PENDING"
	AgentSessionRunning     AgentSessionStatus = "RUNNING"
	AgentSessionCompleted   AgentSessionStatus = "COMPLETED"
	AgentSessionFailed      AgentSessionStatus = "FAILED"
	AgentWaitingApproval    AgentSessionStatus = "WAITING_APPROVAL"
	AgentWaitingRevision    AgentSessionStatus = "WAITING_REVISION"
	AgentRevisionInProgress AgentSessionStatus = "REVISION_IN_PROGRESS"
)

type AgentMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
}

type AgentSession struct {
	ID          string             `json:"id"`
	AgentType   string             `json:"agent_type"`
	Status      AgentSessionStatus `json:"status"`
	HistoryFile string             `json:"history_file"`
	Messages    []AgentMessage     `json:"messages,omitempty"`
}

type SubtaskAgent struct {
	SubtaskID  string            `json:"subtask_id"`
	Session    AgentSession      `json:"session"`
	Context    map[string]string `json:"context"`
	WorkDir    string            `json:"work_dir"`
	PromptFile string            `json:"prompt_file"`
	OutFile    string            `json:"out_file"`
	ExitFile   string            `json:"exit_file"`
}

type SubtaskMessage struct {
	SubtaskID string `json:"subtask_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Content   string `json:"content"`
}

type NotificationStatus string

const (
	NotificationStarted   NotificationStatus = "STARTED"
	NotificationCompleted NotificationStatus = "COMPLETED"
	NotificationFailed    NotificationStatus = "FAILED"
)

type NodeNotification struct {
	RunID     string             `json:"run_id"`
	NodeName  string             `json:"node_name"`
	Status    NotificationStatus `json:"status"`
	Timestamp string             `json:"timestamp"`
}

type ApprovalRequest struct {
	RunID     string `json:"run_id,omitempty"`
	SessionID string `json:"session_id"`
	NodeName  string `json:"node_name"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

const (
	RejectTypeReplan        = "replan"
	RejectTypeResubtask     = "resubtask"
	RejectTypeReviseSubtask = "revise_subtask"
)

type ReviewDecision struct {
	Approved    bool   `json:"approved"`
	Reason      string `json:"reason"`
	RejectType  string `json:"reject_type"`
	ResumePoint string `json:"resume_point"`
	SessionID   string `json:"session_id"`
	NodeName    string `json:"node_name"`
	Timestamp   string `json:"timestamp"`
}

// PhaseStatus 代表一个 run 的当前阶段状态
type PhaseStatus struct {
	RunID            string    `json:"run_id"`
	Phase            string    `json:"phase"`       // clarification/planning/development/evaluation/approval/completed
	GateStatus       string    `json:"gate_status"` // blocked/passed/failed/waiting_user
	FailureClass     string    `json:"failure_class,omitempty"`
	NextAction       string    `json:"next_action"` // run_evaluator/retry_generator/ask_user/finish
	NextActionPrompt string    `json:"next_action_prompt,omitempty"`
	MissingItems     []string  `json:"missing_items,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CompletionCheck 代表一个 run 的完成证据检查结果
type CompletionCheck struct {
	RunID         string         `json:"run_id"`
	CheckedAt     time.Time      `json:"checked_at"`
	Passed        bool           `json:"passed"`
	EvidenceIndex []EvidenceItem `json:"evidence_index,omitempty"`
	Missing       []string       `json:"missing,omitempty"`
	BlockedItems  []BlockedItem  `json:"blocked_items,omitempty"`
}

// EvidenceItem 是单个 evidence 条目
type EvidenceItem struct {
	SubtaskID    string `json:"subtask_id"`
	EvidenceType string `json:"evidence_type"` // build_log/test_log/logcat/screenshot
	Path         string `json:"path"`
	Verified     bool   `json:"verified"`
}

// BlockedItem 是被阻塞的项
type BlockedItem struct {
	SubtaskID string `json:"subtask_id"`
	Reason    string `json:"reason"`
}

// ReuseAck records what knowledge was applied vs ignored at a phase gate.
type ReuseAck struct {
	RunID     string      `json:"run_id"`
	Phase     string      `json:"phase"`
	Timestamp time.Time   `json:"timestamp"`
	Applied   []ReuseHit  `json:"applied"`
	Ignored   []ReuseMiss `json:"ignored"`
	Accepted  bool        `json:"accepted"`
	Note      string      `json:"note,omitempty"`
}

type ReuseHit struct {
	MemoryID string  `json:"memory_id"`
	Title    string  `json:"title"`
	Score    float64 `json:"score"`
	WhyUsed  string  `json:"why_used,omitempty"`
}

type ReuseMiss struct {
	MemoryID   string `json:"memory_id"`
	Title      string `json:"title"`
	WhyIgnored string `json:"why_ignored,omitempty"`
}

// EvaluationResult 是 Node 4 (Review/验证阶段) 的结构化输出
type EvaluationResult struct {
	RunID          string           `json:"run_id"`
	EvaluatedAt    time.Time        `json:"evaluated_at"`
	TestResults    []TestResult     `json:"test_results,omitempty"`
	Evidence       []EvidenceRef    `json:"evidence,omitempty"`
	FailureClass   string           `json:"failure_class,omitempty"` // 见下方分类
	RetryAdvice    string           `json:"retry_advice,omitempty"`
	Passed         bool             `json:"passed"`
	SubtaskResults []SubtaskFailure `json:"subtask_results,omitempty"`
}

// TestResult 是单个验证命令的执行结果
type TestResult struct {
	TestID       string `json:"test_id"`
	SubtaskID    string `json:"subtask_id,omitempty"`
	Command      string `json:"command"`
	ExitCode     int    `json:"exit_code"`
	Passed       bool   `json:"passed"`
	StdoutPath   string `json:"stdout_path,omitempty"`
	StderrPath   string `json:"stderr_path,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	FailureClass string `json:"failure_class,omitempty"`
}

type SubtaskFailure struct {
	SubtaskID    string        `json:"subtask_id"`
	FailureClass FailureClass  `json:"failure_class"`
	Reason       string        `json:"reason"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
	RetryAdvice  string        `json:"retry_advice,omitempty"`
	NextAction   string        `json:"next_action,omitempty"`
}

// EvidenceRef 是对 evidence 文件的引用
type EvidenceRef struct {
	Type string `json:"type"` // build_log/test_log/coverage/screenshot
	Path string `json:"path"`
}

// FailureClass 可能的值：
//   "none"             — 无失败
//   "product_defect"   — 代码本身有 bug，验证失败是预期的（真问题）
//   "environment_block"— 环境问题（依赖没装/网络不通/权限不足），验证失败不是代码问题
//   "validator_issue"  — 验证工具本身有问题（测试框架崩/断言写错/超时）
//   "transient"        — 偶发不稳定（flaky test），重试可能好
//   "missing_evidence" — 缺少必要的验证证据，无法判断
//   "inconclusive"     — 结果模糊，无法分类
