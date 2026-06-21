package adapter

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// APIAdapter invokes an external HTTP API agent.
type APIAdapter struct {
	BaseURL string
	APIKey  string
}

func (a APIAdapter) Key() string { return "api" }

func (a APIAdapter) Invoke(req AgentRequest) (*AgentResult, error) {
	// req.RawJSON is forwarded as-is to the configured API endpoint.
	// A real implementation would HTTP POST to a.BaseURL with a.APIKey header.
	_ = a.BaseURL
	_ = a.APIKey
	if len(req.RawJSON) == 0 {
		return &AgentResult{Output: req.Prompt, Summary: "echo: " + req.Prompt}, nil
	}
	return &AgentResult{RawJSON: req.RawJSON, Summary: "api adapter response"}, nil
}

// ExecAdapter runs a command-line agent.
type ExecAdapter struct {
	Command string
	Args    []string
}

func (a ExecAdapter) Key() string { return "exec" }

func (a ExecAdapter) Invoke(req AgentRequest) (*AgentResult, error) {
	cmd := a.Command
	args := a.Args
	if len(req.RawJSON) > 0 {
		args = append(args, string(req.RawJSON))
	} else if req.Prompt != "" {
		args = append(args, req.Prompt)
	}
	out, err := exec.Command(cmd, args...).Output()
	if err != nil {
		return &AgentResult{Error: err.Error()}, fmt.Errorf("exec adapter: %w", err)
	}
	return &AgentResult{Output: string(out), Summary: "exec adapter done"}, nil
}

// FakeAdapter is a no-op adapter for testing.
type FakeAdapter struct{}

func (a FakeAdapter) Key() string { return "fake" }

func (a FakeAdapter) Invoke(req AgentRequest) (*AgentResult, error) {
	raw, _ := json.Marshal(req)
	return &AgentResult{
		Output:  "fake output for: " + req.Prompt,
		RawJSON: raw,
		Summary: "fake adapter response",
	}, nil
}