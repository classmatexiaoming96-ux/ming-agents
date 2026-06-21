package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	claudeCodeManagers sync.Map
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

	// Close session if using a per-invoke manager (custom Command).
	if a.Command != "" {
		session.Close()
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
		manager, _ := claudeCodeManagers.LoadOrStore(claudeCodeManagerKey(config), NewClaudeCodeSessionManager(config))
		return manager.(*ClaudeCodeSessionManager)
	}
	manager, _ := claudeCodeManagers.LoadOrStore(claudeCodeManagerKey(config), NewClaudeCodeSessionManager(config))
	return manager.(*ClaudeCodeSessionManager)
}

func claudeCodeManagerKey(config ClaudeCodeConfig) string {
	normalized := NewClaudeCodeSessionManager(config).config
	raw, _ := json.Marshal(struct {
		Command        string `json:"command"`
		InvokeTimeout  string `json:"invoke_timeout"`
		StartupTimeout string `json:"startup_timeout"`
		ReadyTimeout   string `json:"ready_timeout"`
	}{
		Command:        normalized.Command,
		InvokeTimeout:  normalized.InvokeTimeout.String(),
		StartupTimeout: normalized.StartupTimeout.String(),
		ReadyTimeout:   normalized.ReadyTimeout.String(),
	})
	sum := sha256.Sum256(raw)
	return normalized.Command + ":" + hex.EncodeToString(sum[:])
}
