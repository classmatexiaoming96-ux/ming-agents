package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/ming-agents/server/adapter"
)

var ptyUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type ptyServerMessage struct {
	Type   string `json:"type"`
	Data   string `json:"data,omitempty"`
	Offset uint64 `json:"offset,omitempty"`
	Error  string `json:"error,omitempty"`
}

type ptyClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (s *Server) handlePTYWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	rec, ok := adapter.DefaultPTYSessionRegistry.Get(sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	reader := rec.Reader()
	if reader == nil {
		http.Error(w, "session reader not available", http.StatusGone)
		return
	}

	conn, err := ptyUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	raw, offset := reader.RawSnapshot()
	if err := conn.WriteJSON(ptyServerMessage{
		Type:   "snapshot",
		Data:   base64.StdEncoding.EncodeToString(raw),
		Offset: offset,
	}); err != nil {
		return
	}

	ch, unsubscribe := reader.Subscribe()
	defer unsubscribe()
	done := make(chan struct{})
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		nextOffset := offset
		for {
			select {
			case <-done:
				return
			case data, ok := <-ch:
				if !ok {
					return
				}
				nextOffset += uint64(len(data))
				if err := conn.WriteJSON(ptyServerMessage{
					Type:   "delta",
					Data:   base64.StdEncoding.EncodeToString(data),
					Offset: nextOffset,
				}); err != nil {
					return
				}
			}
		}
	}()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg ptyClientMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "input":
			data, err := decodePTYInput(msg.Data)
			if err != nil {
				continue
			}
			_ = rec.WriteInput(data)
		case "resize":
			_ = rec.Resize(msg.Cols, msg.Rows)
		}
	}
	close(done)
	<-writeDone
}

func decodePTYInput(data string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		return decoded, nil
	}
	return []byte(data), nil
}
