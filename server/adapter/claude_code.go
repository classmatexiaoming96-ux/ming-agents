package adapter

import "time"

// ClaudeCodeAdapter invokes Claude Code with `claude --acp --stdio`.
type ClaudeCodeAdapter struct {
	Command string
	WorkDir string
	Timeout time.Duration
}

func (a ClaudeCodeAdapter) Key() string { return "claude-code" }

func (a ClaudeCodeAdapter) Invoke(req AgentRequest) (*AgentResult, error) {
	command := a.Command
	if command == "" {
		command = "claude"
	}
	return runAgentCommand(a.Key(), a.WorkDir, a.Timeout, command, []string{"--acp", "--stdio"}, promptFromRequest(req))
}
