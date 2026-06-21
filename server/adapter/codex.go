package adapter

import "time"

// CodexAdapter invokes Codex CLI tasks with `codex exec <prompt>`.
type CodexAdapter struct {
	Command string
	WorkDir string
	Timeout time.Duration
}

func (a CodexAdapter) Key() string { return "codex" }

func (a CodexAdapter) Invoke(req AgentRequest) (*AgentResult, error) {
	command := a.Command
	if command == "" {
		command = "codex"
	}
	return runAgentCommand(a.Key(), a.WorkDir, a.Timeout, command, []string{"exec", promptFromRequest(req)}, "")
}
