package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
)

func TestSSEManagerBroadcastsOnlyToMatchingRun(t *testing.T) {
	manager := NewSSEManager()
	runID := uuid.New()
	otherRunID := uuid.New()
	client := manager.Subscribe(runID)
	otherClient := manager.Subscribe(otherRunID)
	defer manager.Unsubscribe(runID, client)
	defer manager.Unsubscribe(otherRunID, otherClient)

	change := store.StepStatusChange{
		RunID:     runID,
		StepID:    uuid.New(),
		StepName:  "review",
		From:      domain.StepStatusRunning,
		To:        domain.StepStatusWaitingUserInput,
		Timestamp: time.Now(),
	}
	manager.Broadcast(change)

	select {
	case got := <-client.events:
		if got.StepName != "review" {
			t.Fatalf("event step = %q, want review", got.StepName)
		}
	case <-time.After(time.Second):
		t.Fatal("matching client did not receive event")
	}
	select {
	case got := <-otherClient.events:
		t.Fatalf("other run client received event: %+v", got)
	default:
	}
}

func TestSSEEventsEndpointStreamsStepStatusChange(t *testing.T) {
	db := newAPITestDB(t)
	st := store.NewStore(db)
	srv := NewServer(st, nil, nil, nil, WithFeishuNotifier(nil))

	run := &domain.Run{Name: "run", Status: domain.RunStatusRunning, MaxParallel: 1}
	if err := st.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	step := &domain.Step{RunID: run.ID, Name: "code_review", StepType: domain.StepTypeTask, Status: domain.StepStatusPending}
	if err := st.CreateStep(step); err != nil {
		t.Fatalf("create step: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/runs/"+run.ID.String()+"/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.ServeHTTP(rec, req)
		close(done)
	}()

	waitForSSESubscriber(t, srv.sse, run.ID)
	if err := st.UpdateStepStatus(step.ID, domain.StepStatusWaitingUserInput); err != nil {
		t.Fatalf("update step status: %v", err)
	}
	deadline := time.After(time.Second)
	for {
		if strings.Contains(rec.Body.String(), "data:") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("SSE body did not receive event: %q", rec.Body.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	line, err := bufio.NewReader(strings.NewReader(rec.Body.String())).ReadString('\n')
	if err != nil {
		t.Fatalf("read SSE line: %v", err)
	}
	data := strings.TrimPrefix(strings.TrimSpace(line), "data: ")
	var event struct {
		Type      string `json:"type"`
		Node      string `json:"node"`
		StepID    string `json:"step_id"`
		From      string `json:"from"`
		To        string `json:"to"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		t.Fatalf("decode SSE data %q: %v", data, err)
	}
	if event.Type != "node_status_change" || event.Node != "code_review" || event.StepID != step.ID.String() {
		t.Fatalf("unexpected event payload: %+v", event)
	}
	if event.From != "pending" || event.To != "waiting_user_input" {
		t.Fatalf("event transition = %s -> %s, want pending -> waiting_user_input", event.From, event.To)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not exit after request cancellation")
	}
}

func TestWebSocketRunEventsEndpointIsNotRegistered(t *testing.T) {
	db := newAPITestDB(t)
	srv := NewServer(store.NewStore(db), nil, nil, nil, WithFeishuNotifier(nil))
	req := httptest.NewRequest(http.MethodGet, "/ws/runs/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func waitForSSESubscriber(t *testing.T, manager *SSEManager, runID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.mu.RLock()
		count := len(manager.clients[runID])
		manager.mu.RUnlock()
		if count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("SSE subscriber was not registered")
}
