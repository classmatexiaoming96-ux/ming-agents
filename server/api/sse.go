package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/ming-agents/server/store"
)

// SSEClient subscribes to status changes for one run.
type SSEClient struct {
	runID  uuid.UUID
	events chan store.StepStatusChange
}

// SSEManager manages Server-Sent Events subscribers by run.
type SSEManager struct {
	mu      sync.RWMutex
	clients map[uuid.UUID][]*SSEClient
}

// NewSSEManager creates an SSE manager.
func NewSSEManager() *SSEManager {
	return &SSEManager{clients: make(map[uuid.UUID][]*SSEClient)}
}

// Subscribe adds a client subscription for a run.
func (m *SSEManager) Subscribe(runID uuid.UUID) *SSEClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	client := &SSEClient{runID: runID, events: make(chan store.StepStatusChange, 100)}
	m.clients[runID] = append(m.clients[runID], client)
	return client
}

// Unsubscribe removes a client subscription.
func (m *SSEManager) Unsubscribe(runID uuid.UUID, client *SSEClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clients := m.clients[runID]
	for i, c := range clients {
		if c == client {
			m.clients[runID] = append(clients[:i], clients[i+1:]...)
			if len(m.clients[runID]) == 0 {
				delete(m.clients, runID)
			}
			close(c.events)
			return
		}
	}
}

// Broadcast sends a status change to all subscribers for its run.
func (m *SSEManager) Broadcast(change store.StepStatusChange) {
	m.mu.RLock()
	clients := append([]*SSEClient(nil), m.clients[change.RunID]...)
	m.mu.RUnlock()
	for _, c := range clients {
		select {
		case c.events <- change:
		default:
		}
	}
}

func (s *Server) handleSSEEvents(w http.ResponseWriter, r *http.Request) {
	runID, err := uuid.Parse(r.PathValue("run_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	client := s.sse.Subscribe(runID)
	defer s.sse.Unsubscribe(runID, client)

	for {
		select {
		case change, ok := <-client.events:
			if !ok {
				return
			}
			data, err := json.Marshal(map[string]any{
				"type":      "node_status_change",
				"node":      change.StepName,
				"step_id":   change.StepID.String(),
				"from":      string(change.From),
				"to":        string(change.To),
				"timestamp": change.Timestamp.Format("2006-01-02T15:04:05Z"),
			})
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}
