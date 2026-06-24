package workflow

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
	Passed  bool          `json:"passed"`
	Summary string        `json:"summary"`
	Issues  []ReviewIssue `json:"issues"`
}

type ReviewIssue struct {
	SubtaskID     string   `json:"subtask_id,omitempty"`
	SessionID     string   `json:"session_id,omitempty"`
	Severity      string   `json:"severity"`
	Description   string   `json:"description"`
	RequiredFixes []string `json:"required_fixes"`
}

type NodeStatus string

const (
	NodePending       NodeStatus = "PENDING"
	NodeRunning       NodeStatus = "RUNNING"
	NodeWaitingReview NodeStatus = "WAITING_REVIEW"
	NodeCompleted     NodeStatus = "COMPLETED"
	NodeFailed        NodeStatus = "FAILED"
)

type RunState struct {
	RunID   string                `json:"run_id"`
	Nodes   map[string]NodeStatus `json:"nodes"`
	Details map[string]any        `json:"details,omitempty"`
}

type AgentSessionStatus string

const (
	AgentSessionPending   AgentSessionStatus = "PENDING"
	AgentSessionRunning   AgentSessionStatus = "RUNNING"
	AgentSessionCompleted AgentSessionStatus = "COMPLETED"
	AgentSessionFailed    AgentSessionStatus = "FAILED"
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
