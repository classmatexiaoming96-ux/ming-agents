package adapter

import "encoding/json"

// AgentAdapter is the interface for invoking an LLM-backed agent.
type AgentAdapter interface {
	// Key returns the adapter key (e.g. "openai", "api", "exec").
	Key() string

	// Invoke sends a request to the agent and returns the result.
	Invoke(req AgentRequest) (*AgentResult, error)
}

// AgentRequest wraps an agent invocation request as opaque JSON.
type AgentRequest struct {
	Model   string          `json:"model,omitempty"`
	Prompt  string          `json:"prompt,omitempty"`
	RawJSON json.RawMessage `json:"raw_json,omitempty"`
}

// AgentResult is the result of an agent invocation.
type AgentResult struct {
	Output  string          `json:"output"`
	RawJSON json.RawMessage `json:"raw_json,omitempty"`
	Error   string          `json:"error,omitempty"`
	Summary string          `json:"summary,omitempty"`
}