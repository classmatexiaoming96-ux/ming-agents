package api

import (
	"net/http"
	"time"

	"github.com/ming-agents/server/adapter"
)

type PTYSessionsResponse struct {
	Sessions []*PTYSessionInfo `json:"sessions"`
}

type PTYSessionInfo struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	StepID    string `json:"stepId"`
	NodeName  string `json:"nodeName"`
	SubtaskID string `json:"subtaskId"`
	AgentType string `json:"agentType"`
	Status    string `json:"status"`
	WorkDir   string `json:"workDir"`
	CreatedAt string `json:"createdAt"`
}

func (s *Server) handlePTYSessions(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	sessions := adapter.DefaultPTYSessionRegistry.ListByRun(runID)
	resp := make([]*PTYSessionInfo, 0, len(sessions))
	for _, session := range sessions {
		createdAt := ""
		if !session.CreatedAt.IsZero() {
			createdAt = session.CreatedAt.Format(time.RFC3339)
		}
		resp = append(resp, &PTYSessionInfo{
			SessionID: session.SessionID,
			RunID:     session.RunID,
			StepID:    session.StepID,
			NodeName:  session.NodeName,
			SubtaskID: session.SubtaskID,
			AgentType: session.AgentType,
			Status:    session.Status,
			WorkDir:   session.WorkDir,
			CreatedAt: createdAt,
		})
	}
	writeJSON(w, http.StatusOK, &PTYSessionsResponse{Sessions: resp})
}
