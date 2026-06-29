package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/store"
)

func TestPTYSessionsEndpointListsSessionsForRun(t *testing.T) {
	db := newAPITestDB(t)
	srv := NewServer(store.NewStore(db), nil, nil, nil, WithFeishuNotifier(nil))
	oldRegistry := adapter.DefaultPTYSessionRegistry
	adapter.DefaultPTYSessionRegistry = adapter.NewPTYSessionRegistry()
	t.Cleanup(func() { adapter.DefaultPTYSessionRegistry = oldRegistry })

	createdAt := time.Now().Add(-time.Minute).UTC()
	adapter.DefaultPTYSessionRegistry.Register(&adapter.PTYSessionRecord{
		SessionID: "session-run",
		RunID:     "run-pty",
		StepID:    "step-1",
		NodeName:  "node_3",
		SubtaskID: "api",
		AgentType: "codex",
		WorkDir:   "/repo/api",
		Status:    adapter.PTYSessionStatusRunning,
		CreatedAt: createdAt,
	})
	adapter.DefaultPTYSessionRegistry.Register(&adapter.PTYSessionRecord{
		SessionID: "session-other",
		RunID:     "other-run",
		Status:    adapter.PTYSessionStatusRunning,
	})

	req := httptest.NewRequest(http.MethodGet, "/runs/run-pty/pty-sessions", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body PTYSessionsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Sessions) != 1 {
		t.Fatalf("sessions length = %d, want 1; body: %+v", len(body.Sessions), body)
	}
	got := body.Sessions[0]
	if got.SessionID != "session-run" || got.RunID != "run-pty" || got.NodeName != "node_3" || got.AgentType != "codex" {
		t.Fatalf("unexpected session info: %+v", got)
	}
	if got.CreatedAt == "" {
		t.Fatal("CreatedAt is empty")
	}
}

func TestPTYWebSocketStreamsSnapshotDeltasAndWritesInput(t *testing.T) {
	db := newAPITestDB(t)
	srv := NewServer(store.NewStore(db), nil, nil, nil, WithFeishuNotifier(nil))
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()
	oldRegistry := adapter.DefaultPTYSessionRegistry
	adapter.DefaultPTYSessionRegistry = adapter.NewPTYSessionRegistry()
	t.Cleanup(func() { adapter.DefaultPTYSessionRegistry = oldRegistry })

	readFile, writeFile, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	reader := adapter.NewPTYReader(readFile)
	go reader.ReadLoop()
	t.Cleanup(reader.Close)
	if _, err := writeFile.Write([]byte("\x1b[31msnapshot\x1b[0m\n")); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	waitForRawSnapshot(t, reader)

	inputCh := make(chan string, 1)
	resizeCh := make(chan [2]int, 1)
	rec := &adapter.PTYSessionRecord{
		SessionID: "session-ws",
		RunID:     "run-ws",
		Status:    adapter.PTYSessionStatusRunning,
	}
	rec.AttachIO(reader, func(data []byte) error {
		inputCh <- string(data)
		return nil
	}, func(cols, rows int) error {
		resizeCh <- [2]int{cols, rows}
		return nil
	})
	adapter.DefaultPTYSessionRegistry.Register(rec)

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/pty/session-ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	var snap struct {
		Type   string `json:"type"`
		Data   string `json:"data"`
		Offset uint64 `json:"offset"`
	}
	if err := conn.ReadJSON(&snap); err != nil {
		t.Fatalf("ReadJSON(snapshot) error = %v", err)
	}
	if snap.Type != "snapshot" {
		t.Fatalf("snapshot type = %q, want snapshot", snap.Type)
	}
	decoded, err := base64.StdEncoding.DecodeString(snap.Data)
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if !strings.Contains(string(decoded), "\x1b[31msnapshot\x1b[0m\n") {
		t.Fatalf("snapshot data = %q, want raw ANSI snapshot", string(decoded))
	}

	if _, err := writeFile.Write([]byte("delta\n")); err != nil {
		t.Fatalf("write delta: %v", err)
	}
	var delta struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}
	if err := conn.ReadJSON(&delta); err != nil {
		t.Fatalf("ReadJSON(delta) error = %v", err)
	}
	if delta.Type != "delta" {
		t.Fatalf("delta type = %q, want delta", delta.Type)
	}
	decoded, err = base64.StdEncoding.DecodeString(delta.Data)
	if err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	if string(decoded) != "delta\n" {
		t.Fatalf("delta data = %q, want delta newline", string(decoded))
	}

	if err := conn.WriteJSON(map[string]any{"type": "input", "data": "2\r"}); err != nil {
		t.Fatalf("WriteJSON(input) error = %v", err)
	}
	select {
	case got := <-inputCh:
		if got != "2\r" {
			t.Fatalf("input = %q, want 2 carriage-return", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for input callback")
	}

	if err := conn.WriteJSON(map[string]any{"type": "resize", "cols": 100, "rows": 30}); err != nil {
		t.Fatalf("WriteJSON(resize) error = %v", err)
	}
	select {
	case got := <-resizeCh:
		if got != [2]int{100, 30} {
			t.Fatalf("resize = %+v, want 100x30", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resize callback")
	}
}

func waitForRawSnapshot(t *testing.T, reader *adapter.PTYReader) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		raw, _ := reader.RawSnapshot()
		if len(raw) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for raw snapshot")
}
