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

// CodexAdapter invokes Codex CLI tasks in interactive PTY mode.
type CodexAdapter struct {
	Command string
	WorkDir string
	Timeout time.Duration
}

func (a CodexAdapter) Key() string { return "codex" }

var (
	codexManagers sync.Map
)

func (a CodexAdapter) Invoke(req AgentRequest, execCtx ...ExecutionContext) (*AgentResult, error) {
	effective := mergeExecutionContext(a.WorkDir, a.Command, a.Timeout, execCtx, req)
	command := effective.Command
	if command == "" {
		command = "codex"
	}

	manager := a.manager(command)
	timeout := effectiveTimeout(effective.Timeout)
	session, err := manager.GetOrStart(context.Background(), effective.WorkDir)
	if err != nil {
		return runAgentCommand(a.Key(), effective.WorkDir, effective.Timeout, command, []string{"exec", promptFromRequest(req)}, "")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output, err := session.SendPrompt(ctx, promptFromRequest(req))
	timedOut := ctx.Err() == context.DeadlineExceeded
	if err != nil {
		if timedOut {
			session.Close()
		}
		result := processErrorResult(a.Key(), effective.WorkDir, []string{command}, -1, timedOut, err.Error())
		return result, fmt.Errorf("%s adapter: %w", a.Key(), err)
	}

	return &AgentResult{
		Output: output,
		RawJSON: marshalProcessResult(processResult{
			Adapter:  a.Key(),
			Command:  []string{command},
			WorkDir:  effective.WorkDir,
			ExitCode: 0,
		}),
		Summary: fmt.Sprintf("%s adapter completed", a.Key()),
	}, nil
}

func (a CodexAdapter) manager(command string) *CodexSessionManager {
	config := CodexConfig{
		Command:       command,
		InvokeTimeout: effectiveTimeout(a.Timeout),
	}
	manager, _ := codexManagers.LoadOrStore(codexManagerKey(config), NewCodexSessionManager(config))
	return manager.(*CodexSessionManager)
}

func codexManagerKey(config CodexConfig) string {
	normalized := NewCodexSessionManager(config).config
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
