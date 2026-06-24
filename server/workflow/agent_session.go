package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var agentSessionRegistry = struct {
	sync.RWMutex
	sessions map[string]AgentSession
}{sessions: map[string]AgentSession{}}

var ErrApprovalRejected = errors.New("approval rejected")

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
		RegisterAgentSession(agent.Session)
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

func RegisterAgentSession(session AgentSession) {
	if session.ID == "" || session.HistoryFile == "" {
		return
	}
	agentSessionRegistry.Lock()
	defer agentSessionRegistry.Unlock()
	agentSessionRegistry.sessions[session.ID] = session
}

func WorkflowNodeSession(repoRoot, runID, nodeName string) AgentSession {
	session := AgentSession{
		ID:          NewPTYSessionID(runID, nodeName, "workflow", 0),
		AgentType:   "workflow",
		Status:      AgentSessionRunning,
		HistoryFile: filepath.Join(repoRoot, ".workflow", "runs", runID, nodeName, nodeName+".messages.jsonl"),
	}
	RegisterAgentSession(session)
	return session
}

func EmitNodeNotification(sessionID string, notification NodeNotification) error {
	session, err := registeredAgentSession(sessionID)
	if err != nil {
		return err
	}
	if notification.Timestamp == "" {
		notification.Timestamp = time.Now().Format(time.RFC3339)
	}
	data, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	agent := &SubtaskAgent{Session: session}
	return AppendAgentMessage(agent, AgentMessage{
		Role:    "notification",
		Content: string(data),
	})
}

func WaitForApproval(ctx context.Context, sessionID, nodeName string) error {
	session, err := registeredAgentSession(sessionID)
	if err != nil {
		return err
	}
	request := ApprovalRequest{
		SessionID: sessionID,
		NodeName:  nodeName,
		Status:    "WAITING",
		Timestamp: time.Now().Format(time.RFC3339),
	}
	if runID := runIDFromSessionID(sessionID); runID != "" {
		request.RunID = runID
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	agent := &SubtaskAgent{Session: session}
	if err := AppendAgentMessage(agent, AgentMessage{Role: "approval_request", Content: string(data)}); err != nil {
		return err
	}

	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		decision, ok, err := latestReviewDecision(session.HistoryFile, sessionID, nodeName)
		if err != nil {
			return err
		}
		if ok {
			if decision.Approved {
				return nil
			}
			return ErrApprovalRejected
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func RejectSession(sessionID, nodeName string, decision ReviewDecision) error {
	session, err := registeredAgentSession(sessionID)
	if err != nil {
		return err
	}
	decision.Approved = false
	if decision.SessionID == "" {
		decision.SessionID = sessionID
	}
	if decision.NodeName == "" {
		decision.NodeName = nodeName
	}
	if decision.Timestamp == "" {
		decision.Timestamp = time.Now().Format(time.RFC3339)
	}
	data, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	agent := &SubtaskAgent{Session: session}
	return AppendAgentMessage(agent, AgentMessage{
		Role:    "rejection",
		Content: string(data),
	})
}

func ApproveSession(sessionID, nodeName, message string) error {
	session, err := registeredAgentSession(sessionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		message = "approved"
	}
	data, err := json.Marshal(ApprovalRequest{
		RunID:     runIDFromSessionID(sessionID),
		SessionID: sessionID,
		NodeName:  nodeName,
		Status:    "APPROVED",
		Timestamp: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	agent := &SubtaskAgent{Session: session}
	return AppendAgentMessage(agent, AgentMessage{
		Role:    "approval",
		Content: string(data) + "\n" + message,
	})
}

func LatestReviewDecision(sessionID, nodeName string) (ReviewDecision, bool, error) {
	session, err := registeredAgentSession(sessionID)
	if err != nil {
		return ReviewDecision{}, false, err
	}
	return latestReviewDecision(session.HistoryFile, sessionID, nodeName)
}

func registeredAgentSession(sessionID string) (AgentSession, error) {
	agentSessionRegistry.RLock()
	defer agentSessionRegistry.RUnlock()
	session, ok := agentSessionRegistry.sessions[sessionID]
	if !ok {
		return AgentSession{}, fmt.Errorf("agent session %q is not registered", sessionID)
	}
	return session, nil
}

func historyHasApproval(path, nodeName string) (bool, error) {
	decision, ok, err := latestReviewDecision(path, "", nodeName)
	if err != nil {
		return false, err
	}
	return ok && decision.Approved, nil
}

func latestReviewDecision(path, sessionID, nodeName string) (ReviewDecision, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ReviewDecision{}, false, nil
	}
	if err != nil {
		return ReviewDecision{}, false, err
	}
	var decision ReviewDecision
	var hasDecision bool
	var waitingForNode bool
	var seenRequest bool
	var preRequestDecision ReviewDecision
	var hasPreRequestDecision bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg AgentMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Role == "approval_request" && messageReferencesReviewTarget(msg.Content, sessionID, nodeName) {
			waitingForNode = true
			if !seenRequest && hasPreRequestDecision {
				decision = preRequestDecision
				hasDecision = true
			} else {
				hasDecision = false
				decision = ReviewDecision{}
			}
			seenRequest = true
			continue
		}
		switch msg.Role {
		case "approval":
			if messageReferencesReviewTarget(msg.Content, sessionID, nodeName) {
				approved := ReviewDecision{
					Approved:   true,
					SessionID:  sessionID,
					NodeName:   nodeName,
					Timestamp:  msg.Timestamp,
					Reason:     strings.TrimSpace(msg.Content),
					RejectType: "",
				}
				if waitingForNode {
					decision = approved
					hasDecision = true
				} else if !seenRequest {
					preRequestDecision = approved
					hasPreRequestDecision = true
				}
			}
		case "rejection":
			var rejected ReviewDecision
			if err := json.Unmarshal([]byte(msg.Content), &rejected); err != nil {
				continue
			}
			if rejected.NodeName == nodeName && decisionReferencesSession(rejected.SessionID, sessionID) {
				if rejected.Timestamp == "" {
					rejected.Timestamp = msg.Timestamp
				}
				if waitingForNode {
					decision = rejected
					hasDecision = true
				} else if !seenRequest {
					preRequestDecision = rejected
					hasPreRequestDecision = true
				}
			}
		}
	}
	return decision, hasDecision, nil
}

func messageReferencesReviewTarget(content, sessionID, nodeName string) bool {
	if request, ok := parseApprovalRequest(content); ok {
		return request.NodeName == nodeName && decisionReferencesSession(request.SessionID, sessionID)
	}
	if !strings.Contains(content, `"node_name":"`+nodeName+`"`) {
		return false
	}
	return sessionID == "" || !strings.Contains(content, `"session_id":`) || strings.Contains(content, `"session_id":"`+sessionID+`"`)
}

func parseApprovalRequest(content string) (ApprovalRequest, bool) {
	var request ApprovalRequest
	if err := json.Unmarshal([]byte(content), &request); err == nil {
		return request, true
	}
	firstLine := content
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		firstLine = content[:idx]
	}
	if err := json.Unmarshal([]byte(firstLine), &request); err == nil {
		return request, true
	}
	return ApprovalRequest{}, false
}

func decisionReferencesSession(decisionSessionID, targetSessionID string) bool {
	return targetSessionID == "" || decisionSessionID == "" || decisionSessionID == targetSessionID
}

func runIDFromSessionID(sessionID string) string {
	const prefix = "pty-"
	trimmed := strings.TrimPrefix(sessionID, prefix)
	if trimmed == sessionID {
		return ""
	}
	if idx := strings.Index(trimmed, "-node"); idx >= 0 {
		return trimmed[:idx]
	}
	return ""
}
