package adapter

import "time"

// CodexAdapter invokes Codex CLI tasks with `codex exec <prompt>`.
type CodexAdapter struct {
	Command string
	WorkDir string
	Timeout time.Duration
}

func (a CodexAdapter) Key() string { return "codex" }

func (a CodexAdapter) Invoke(req AgentRequest, execCtx ...ExecutionContext) (*AgentResult, error) {
	effective := mergeExecutionContext(a.WorkDir, a.Command, a.Timeout, execCtx, req)
	command := effective.Command
	if command == "" {
		command = "codex"
	}
	return runAgentCommand(a.Key(), effective.WorkDir, effective.Timeout, command, []string{"exec", promptFromRequest(req)}, "")
}
