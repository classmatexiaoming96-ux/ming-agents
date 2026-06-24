package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func BuildSubtaskAgents(repoRoot, nodeDir string, plan *Plan) ([]SubtaskAgent, error) {
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	agents := make([]SubtaskAgent, 0, len(plan.Subtasks))
	for i, st := range plan.Subtasks {
		base := fmt.Sprintf("dev-%d", i+1)
		sessionID := NewPTYSessionID(plan.TaskID, "node3", "subtask-"+st.ID, i+1)
		agent := SubtaskAgent{
			SubtaskID: st.ID,
			Session: AgentSession{
				ID:          sessionID,
				AgentType:   st.AgentType,
				Status:      AgentSessionPending,
				HistoryFile: filepath.Join(nodeDir, "agents", st.ID+".messages.jsonl"),
			},
			Context: map[string]string{
				"task_id":     plan.TaskID,
				"subtask_id":  st.ID,
				"repo_path":   st.RepoPath,
				"description": st.Description,
			},
			WorkDir:    filepath.Join(repoRoot, filepath.Clean(st.RepoPath)),
			PromptFile: filepath.Join(nodeDir, base+".prompt.md"),
			OutFile:    filepath.Join(nodeDir, base+".out.md"),
			ExitFile:   filepath.Join(nodeDir, base+".exit"),
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

func WriteSubtaskAgentManifest(path string, agents []SubtaskAgent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return writeJSONAtomic(path, agents)
}

func RouteSubtaskMessage(agents []SubtaskAgent, msg SubtaskMessage) (*SubtaskAgent, error) {
	if len(agents) == 0 {
		return nil, errors.New("no subtask agents available")
	}
	if msg.SubtaskID != "" {
		return findSubtaskAgent(agents, func(agent SubtaskAgent) bool {
			return agent.SubtaskID == msg.SubtaskID
		}, "subtask_id", msg.SubtaskID)
	}
	if msg.SessionID != "" {
		return findSubtaskAgent(agents, func(agent SubtaskAgent) bool {
			return agent.Session.ID == msg.SessionID
		}, "session_id", msg.SessionID)
	}

	content := strings.ToLower(msg.Content)
	var matches []int
	for i, agent := range agents {
		if strings.Contains(content, strings.ToLower(agent.SubtaskID)) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 1 {
		return &agents[matches[0]], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("message matches multiple subtask agents")
	}
	return nil, errors.New("message does not identify a subtask agent")
}

func findSubtaskAgent(agents []SubtaskAgent, match func(SubtaskAgent) bool, key, value string) (*SubtaskAgent, error) {
	for i := range agents {
		if match(agents[i]) {
			return &agents[i], nil
		}
	}
	return nil, fmt.Errorf("no subtask agent for %s %q", key, value)
}

func AppendAgentMessage(agent *SubtaskAgent, msg AgentMessage) error {
	if agent == nil {
		return errors.New("subtask agent is required")
	}
	if strings.TrimSpace(msg.Role) == "" {
		return errors.New("agent message role is required")
	}
	if strings.TrimSpace(msg.Content) == "" {
		return errors.New("agent message content is required")
	}
	if msg.Timestamp == "" {
		msg.Timestamp = time.Now().Format(time.RFC3339)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(agent.Session.HistoryFile), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(agent.Session.HistoryFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	agent.Session.Messages = append(agent.Session.Messages, msg)
	return nil
}
