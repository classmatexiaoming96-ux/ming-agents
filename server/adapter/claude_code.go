package adapter

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ClaudeCodeAdapter invokes Claude Code in interactive PTY mode.
type ClaudeCodeAdapter struct {
	Command string
	WorkDir string
	Timeout time.Duration
}

func (a ClaudeCodeAdapter) Key() string { return "claude-code" }

var (
	claudeCodeManagerOnce sync.Once
	claudeCodeManager     *ClaudeCodeSessionManager
)

func (a ClaudeCodeAdapter) Invoke(req AgentRequest) (*AgentResult, error) {
	command := a.Command
	if command == "" {
		command = "claude"
	}

	manager := a.manager(command)
	timeout := effectiveTimeout(a.Timeout)
	session, err := manager.GetOrStart(context.Background(), a.WorkDir)
	if err != nil {
		result := processErrorResult(a.Key(), a.WorkDir, []string{command}, -1, false, err.Error())
		return result, fmt.Errorf("%s adapter: %w", a.Key(), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output, err := session.SendPrompt(ctx, promptFromRequest(req))
	timedOut := ctx.Err() == context.DeadlineExceeded
	if err != nil {
		if timedOut {
			session.Close()
		}
		result := processErrorResult(a.Key(), a.WorkDir, []string{command}, -1, timedOut, err.Error())
		return result, fmt.Errorf("%s adapter: %w", a.Key(), err)
	}

	return &AgentResult{
		Output: output,
		RawJSON: marshalProcessResult(processResult{
			Adapter:  a.Key(),
			Command:  []string{command},
			WorkDir:  a.WorkDir,
			ExitCode: 0,
		}),
		Summary: fmt.Sprintf("%s adapter completed", a.Key()),
	}, nil
}

func (a ClaudeCodeAdapter) manager(command string) *ClaudeCodeSessionManager {
	config := ClaudeCodeConfig{
		Command:       command,
		InvokeTimeout: effectiveTimeout(a.Timeout),
	}
	if a.Command != "" {
		return NewClaudeCodeSessionManager(config)
	}
	claudeCodeManagerOnce.Do(func() {
		claudeCodeManager = NewClaudeCodeSessionManager(config)
	})
	return claudeCodeManager
}
